package output

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"
)

const (
	defaultMaxFileBytes  = int64(512 << 20)
	defaultMaxTotalBytes = int64(1 << 30)
)

var ErrByteLimit = errors.New("output exceeds byte limit")

type Descriptor struct {
	Filename  string `json:"filename"`
	Subfolder string `json:"subfolder,omitempty"`
	Type      string `json:"type,omitempty"`
	MediaType string `json:"media_type,omitempty"`
	NodeID    string `json:"node_id,omitempty"`
}

type FetchInfo struct {
	Partial bool
}

type Source interface {
	Fetch(ctx context.Context, descriptor Descriptor, offset int64) (io.ReadCloser, FetchInfo, error)
}

type Limits struct {
	MaxFileBytes  int64
	MaxTotalBytes int64
	MaxFiles      int
}

type Asset struct {
	Descriptor
	Path   string `json:"path"`
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256"`
}

func Collect(ctx context.Context, source Source, root string, descriptors []Descriptor, limits Limits) ([]Asset, error) {
	if source == nil {
		return nil, errors.New("output source is required")
	}
	root, err := secureRoot(root)
	if err != nil {
		return nil, err
	}
	if limits.MaxFileBytes <= 0 {
		limits.MaxFileBytes = defaultMaxFileBytes
	}
	if limits.MaxTotalBytes <= 0 {
		limits.MaxTotalBytes = defaultMaxTotalBytes
	}
	if limits.MaxFiles <= 0 {
		limits.MaxFiles = 256
	}
	if len(descriptors) > limits.MaxFiles {
		return nil, fmt.Errorf("output count %d exceeds limit %d", len(descriptors), limits.MaxFiles)
	}
	assets := make([]Asset, 0, len(descriptors))
	var total int64
	for _, descriptor := range descriptors {
		asset, err := collectOne(ctx, source, root, descriptor, limits.MaxFileBytes, limits.MaxTotalBytes-total)
		if err != nil {
			return nil, err
		}
		total += asset.Size
		assets = append(assets, asset)
	}
	return assets, nil
}

func secureRoot(root string) (string, error) {
	if strings.TrimSpace(root) == "" {
		return "", errors.New("output root is required")
	}
	absolute, err := filepath.Abs(root)
	if err != nil {
		return "", fmt.Errorf("resolve output root: %w", err)
	}
	if err := os.MkdirAll(absolute, 0o755); err != nil {
		return "", fmt.Errorf("create output root: %w", err)
	}
	canonical, err := filepath.EvalSymlinks(absolute)
	if err != nil {
		return "", fmt.Errorf("canonicalize output root: %w", err)
	}
	return canonical, nil
}

func safeRelative(descriptor Descriptor) (string, error) {
	filename := strings.TrimSpace(descriptor.Filename)
	if filename == "" {
		return "", errors.New("output filename is empty")
	}
	normalizedName := strings.ReplaceAll(filename, `\`, "/")
	if path.IsAbs(normalizedName) || path.Base(normalizedName) != normalizedName || normalizedName == "." || normalizedName == ".." {
		return "", fmt.Errorf("unsafe output filename %q", filename)
	}
	normalizedFolder := strings.ReplaceAll(strings.TrimSpace(descriptor.Subfolder), `\`, "/")
	if normalizedFolder == "" || normalizedFolder == "." {
		return filename, nil
	}
	cleanFolder := path.Clean(normalizedFolder)
	if path.IsAbs(cleanFolder) || cleanFolder == ".." || strings.HasPrefix(cleanFolder, "../") {
		return "", fmt.Errorf("unsafe output subfolder %q", descriptor.Subfolder)
	}
	return filepath.FromSlash(path.Join(cleanFolder, normalizedName)), nil
}

func collectOne(ctx context.Context, source Source, root string, descriptor Descriptor, maxFile, remaining int64) (Asset, error) {
	if remaining <= 0 {
		return Asset{}, errors.New("aggregate output byte limit exceeded")
	}
	relative, err := safeRelative(descriptor)
	if err != nil {
		return Asset{}, err
	}
	directory, err := secureDirectory(root, filepath.Dir(relative))
	if err != nil {
		return Asset{}, err
	}
	filename := filepath.Base(relative)
	target := filepath.Join(directory, filename)
	if err := rejectSymlink(target); err != nil {
		return Asset{}, err
	}
	partial := filepath.Join(directory, "."+filename+".cmfy-part")
	if err := rejectSymlink(partial); err != nil {
		return Asset{}, err
	}
	var offset int64
	if stat, err := os.Stat(partial); err == nil {
		offset = stat.Size()
	} else if !errors.Is(err, os.ErrNotExist) {
		return Asset{}, fmt.Errorf("inspect partial output: %w", err)
	}
	body, info, err := source.Fetch(ctx, descriptor, offset)
	if err != nil {
		return Asset{}, fmt.Errorf("fetch output %q: %w", descriptor.Filename, err)
	}
	defer body.Close()
	flags := os.O_CREATE | os.O_WRONLY
	if offset > 0 && info.Partial {
		flags |= os.O_APPEND
	} else {
		flags |= os.O_TRUNC
		offset = 0
	}
	file, err := os.OpenFile(partial, flags, 0o600)
	if err != nil {
		return Asset{}, fmt.Errorf("open partial output: %w", err)
	}
	allowed := minInt64(maxFile, remaining)
	written, copyErr := copyBounded(file, body, allowed-offset)
	syncErr := file.Sync()
	closeErr := file.Close()
	if copyErr != nil {
		if errors.Is(copyErr, ErrByteLimit) {
			_ = os.Remove(partial)
			_ = syncDirectory(directory)
		}
		return Asset{}, fmt.Errorf("download output %q: %w", descriptor.Filename, copyErr)
	}
	if syncErr != nil {
		return Asset{}, fmt.Errorf("sync output %q: %w", descriptor.Filename, syncErr)
	}
	if closeErr != nil {
		return Asset{}, fmt.Errorf("close output %q: %w", descriptor.Filename, closeErr)
	}
	size := offset + written
	if size > maxFile || size > remaining {
		_ = os.Remove(partial)
		return Asset{}, fmt.Errorf("output %q exceeds byte limit", descriptor.Filename)
	}
	digest, err := hashFile(partial)
	if err != nil {
		return Asset{}, err
	}
	finalTarget, reused, err := collisionTarget(target, digest)
	if err != nil {
		return Asset{}, err
	}
	if reused {
		if err := os.Remove(partial); err != nil && !errors.Is(err, os.ErrNotExist) {
			return Asset{}, fmt.Errorf("remove duplicate partial output: %w", err)
		}
		if err := syncDirectory(directory); err != nil {
			return Asset{}, err
		}
	} else {
		if err := os.Chmod(partial, 0o644); err != nil {
			return Asset{}, fmt.Errorf("set output permissions: %w", err)
		}
		if err := os.Link(partial, finalTarget); err != nil {
			return Asset{}, fmt.Errorf("atomically publish output %q without overwriting: %w", descriptor.Filename, err)
		}
		if err := os.Remove(partial); err != nil {
			return Asset{}, fmt.Errorf("remove published partial output: %w", err)
		}
		if err := syncDirectory(directory); err != nil {
			return Asset{}, err
		}
	}
	finalRelative, err := filepath.Rel(root, finalTarget)
	if err != nil || finalRelative == ".." || strings.HasPrefix(finalRelative, ".."+string(filepath.Separator)) {
		return Asset{}, errors.New("materialized output escaped root")
	}
	return Asset{Descriptor: descriptor, Path: finalRelative, Size: size, SHA256: digest}, nil
}

func secureDirectory(root, relative string) (string, error) {
	current := root
	if relative == "." || relative == "" {
		return current, nil
	}
	for _, component := range strings.Split(filepath.Clean(relative), string(filepath.Separator)) {
		if component == "" || component == "." || component == ".." {
			return "", errors.New("unsafe output directory")
		}
		current = filepath.Join(current, component)
		info, err := os.Lstat(current)
		if errors.Is(err, os.ErrNotExist) {
			if err := os.Mkdir(current, 0o755); err != nil {
				return "", fmt.Errorf("create output directory: %w", err)
			}
			continue
		}
		if err != nil {
			return "", fmt.Errorf("inspect output directory: %w", err)
		}
		if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
			return "", fmt.Errorf("output directory %q is not a real directory", component)
		}
	}
	return current, nil
}

func rejectSymlink(path string) error {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("inspect output path: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("output path %q is a symlink", path)
	}
	return nil
}

func copyBounded(destination io.Writer, source io.Reader, remaining int64) (int64, error) {
	if remaining < 0 {
		return 0, ErrByteLimit
	}
	limited := io.LimitReader(source, remaining+1)
	written, err := io.Copy(destination, limited)
	if err != nil {
		return written, err
	}
	if written > remaining {
		return written, ErrByteLimit
	}
	return written, nil
}

func hashFile(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", fmt.Errorf("hash output: %w", err)
	}
	defer file.Close()
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return "", fmt.Errorf("hash output: %w", err)
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

func collisionTarget(target, digest string) (string, bool, error) {
	if _, err := os.Stat(target); errors.Is(err, os.ErrNotExist) {
		return target, false, nil
	} else if err != nil {
		return "", false, fmt.Errorf("inspect output collision: %w", err)
	}
	existingDigest, err := hashFile(target)
	if err != nil {
		return "", false, err
	}
	if existingDigest == digest {
		return target, true, nil
	}
	extension := filepath.Ext(target)
	stem := strings.TrimSuffix(target, extension)
	candidate := stem + "-" + digest[:8] + extension
	if _, err := os.Stat(candidate); errors.Is(err, os.ErrNotExist) {
		return candidate, false, nil
	} else if err != nil {
		return "", false, fmt.Errorf("inspect output collision: %w", err)
	}
	candidateDigest, err := hashFile(candidate)
	if err != nil {
		return "", false, err
	}
	if candidateDigest == digest {
		return candidate, true, nil
	}
	return "", false, fmt.Errorf("deterministic collision target %q already contains different data", candidate)
}

func syncDirectory(path string) error {
	directory, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("open output directory for sync: %w", err)
	}
	defer directory.Close()
	if err := directory.Sync(); err != nil {
		return fmt.Errorf("sync output directory: %w", err)
	}
	return nil
}

func minInt64(a, b int64) int64 {
	if a < b {
		return a
	}
	return b
}
