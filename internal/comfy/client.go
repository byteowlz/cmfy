package comfy

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"os"
	"path"
	"strings"
	"time"

	"cmfy/internal/output"
)

const (
	defaultMaxJSONBytes   = int64(8 << 20)
	defaultMaxErrorBytes  = int64(64 << 10)
	defaultMaxUploadBytes = int64(2 << 30)
	defaultMaxOutputBytes = int64(2 << 30)
)

var ErrServerResponse = errors.New("ComfyUI server response error")

type ClientOptions struct {
	HTTPClient     *http.Client
	Timeout        time.Duration
	MaxJSONBytes   int64
	MaxErrorBytes  int64
	MaxUploadBytes int64
	MaxOutputBytes int64
	MaxEventBytes  int64
}

type Client struct {
	BaseURL string
	HTTP    *http.Client
	base    *url.URL
	options ClientOptions
	initErr error
}

type responseError struct {
	operation string
	status    int
	detail    string
}

func (e *responseError) Error() string {
	if e.detail == "" {
		return fmt.Sprintf("%s failed with HTTP %d", e.operation, e.status)
	}
	return fmt.Sprintf("%s failed with HTTP %d: %s", e.operation, e.status, e.detail)
}

func (e *responseError) Unwrap() error { return ErrServerResponse }

func NewClient(baseURL string) *Client {
	client, err := NewClientWithOptions(baseURL, ClientOptions{})
	if err != nil {
		return &Client{BaseURL: strings.TrimRight(baseURL, "/"), initErr: err}
	}
	return client
}

func NewClientWithOptions(baseURL string, options ClientOptions) (*Client, error) {
	parsed, err := url.Parse(strings.TrimSpace(baseURL))
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return nil, fmt.Errorf("invalid ComfyUI server URL")
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return nil, fmt.Errorf("ComfyUI server URL must use http or https")
	}
	if parsed.User != nil {
		return nil, fmt.Errorf("ComfyUI server URL must not contain credentials")
	}
	if parsed.Path != "" && parsed.Path != "/" {
		return nil, fmt.Errorf("ComfyUI server URL must not contain a path")
	}
	if parsed.RawQuery != "" || parsed.Fragment != "" {
		return nil, fmt.Errorf("ComfyUI server URL must not contain a query or fragment")
	}
	parsed.Path = ""
	if options.Timeout <= 0 {
		options.Timeout = 60 * time.Second
	}
	if options.MaxJSONBytes <= 0 {
		options.MaxJSONBytes = defaultMaxJSONBytes
	}
	if options.MaxErrorBytes <= 0 {
		options.MaxErrorBytes = defaultMaxErrorBytes
	}
	if options.MaxUploadBytes <= 0 {
		options.MaxUploadBytes = defaultMaxUploadBytes
	}
	if options.MaxOutputBytes <= 0 {
		options.MaxOutputBytes = defaultMaxOutputBytes
	}
	if options.MaxEventBytes <= 0 {
		options.MaxEventBytes = 1 << 20
	}
	httpClient := options.HTTPClient
	if httpClient == nil {
		httpClient = &http.Client{Timeout: options.Timeout}
	} else {
		clone := *httpClient
		httpClient = &clone
		if httpClient.Timeout <= 0 {
			httpClient.Timeout = options.Timeout
		}
	}
	httpClient.CheckRedirect = func(request *http.Request, via []*http.Request) error {
		if request.URL.Scheme != parsed.Scheme || !strings.EqualFold(request.URL.Host, parsed.Host) {
			return errors.New("cross-origin redirect rejected")
		}
		if len(via) >= 10 {
			return errors.New("too many redirects")
		}
		return nil
	}
	return &Client{
		BaseURL: strings.TrimRight(parsed.String(), "/"),
		HTTP:    httpClient,
		base:    parsed,
		options: options,
	}, nil
}

func (c *Client) Ping() error { return c.PingContext(context.Background()) }

func (c *Client) PingContext(ctx context.Context) error {
	response, err := c.do(ctx, http.MethodGet, "/system_stats", nil, "server ping")
	if err != nil {
		return err
	}
	return response.Body.Close()
}

func (c *Client) Upload(filePath string) (string, error) {
	return c.UploadContext(context.Background(), filePath)
}

func (c *Client) UploadContext(ctx context.Context, filePath string) (string, error) {
	file, err := os.Open(filePath)
	if err != nil {
		return "", err
	}
	stat, err := file.Stat()
	if err != nil {
		file.Close()
		return "", err
	}
	if !stat.Mode().IsRegular() {
		file.Close()
		return "", errors.New("upload source is not a regular file")
	}
	if stat.Size() > c.maxUploadBytes() {
		file.Close()
		return "", fmt.Errorf("upload byte limit exceeded: %d > %d", stat.Size(), c.maxUploadBytes())
	}
	reader, writer := io.Pipe()
	multipartWriter := multipart.NewWriter(writer)
	go func() {
		defer file.Close()
		if err := multipartWriter.WriteField("type", "input"); err != nil {
			_ = writer.CloseWithError(err)
			return
		}
		part, err := multipartWriter.CreateFormFile("image", path.Base(filePath))
		if err != nil {
			_ = writer.CloseWithError(err)
			return
		}
		if _, err := io.Copy(part, file); err != nil {
			_ = writer.CloseWithError(err)
			return
		}
		if err := multipartWriter.Close(); err != nil {
			_ = writer.CloseWithError(err)
			return
		}
		_ = writer.Close()
	}()
	response, err := c.do(ctx, http.MethodPost, "/upload/image", reader, "upload")
	if err != nil {
		return "", err
	}
	defer response.Body.Close()
	responseBody, err := readBounded(response.Body, c.maxJSONBytes(), "upload response")
	if err != nil {
		return "", err
	}
	if len(responseBody) == 0 {
		return path.Base(filePath), nil
	}
	var object struct {
		Name string `json:"name"`
	}
	if err := json.Unmarshal(responseBody, &object); err == nil && object.Name != "" {
		return object.Name, nil
	}
	var names []string
	if err := json.Unmarshal(responseBody, &names); err == nil && len(names) > 0 && names[0] != "" {
		return names[0], nil
	}
	return "", errors.New("upload response contained no filename")
}

func (c *Client) Prompt(clientID string, prompt map[string]interface{}) (string, error) {
	return c.PromptContext(context.Background(), clientID, prompt)
}

func (c *Client) PromptContext(ctx context.Context, clientID string, prompt map[string]interface{}) (string, error) {
	body, err := json.Marshal(map[string]interface{}{"client_id": clientID, "prompt": prompt})
	if err != nil {
		return "", fmt.Errorf("encode prompt: %w", err)
	}
	response, err := c.doJSON(ctx, http.MethodPost, "/prompt", body, "prompt submit")
	if err != nil {
		return "", err
	}
	defer response.Body.Close()
	var result struct {
		PromptID string `json:"prompt_id"`
	}
	if err := decodeBounded(response.Body, c.maxJSONBytes(), &result, "prompt response"); err != nil {
		return "", err
	}
	if result.PromptID == "" {
		return "", errors.New("prompt response contained an empty prompt_id")
	}
	return result.PromptID, nil
}

func (c *Client) History(promptID string) (map[string]interface{}, error) {
	return c.HistoryContext(context.Background(), promptID)
}

func (c *Client) HistoryContext(ctx context.Context, promptID string) (map[string]interface{}, error) {
	var result map[string]interface{}
	response, err := c.do(ctx, http.MethodGet, "/history/"+url.PathEscape(promptID), nil, "history")
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if err := decodeBounded(response.Body, c.maxJSONBytes(), &result, "history response"); err != nil {
		return nil, err
	}
	return result, nil
}

func (c *Client) HistoryAllContext(ctx context.Context) (map[string]interface{}, error) {
	var result map[string]interface{}
	response, err := c.do(ctx, http.MethodGet, "/history", nil, "history list")
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if err := decodeBounded(response.Body, c.maxJSONBytes(), &result, "history list response"); err != nil {
		return nil, err
	}
	return result, nil
}

func (c *Client) Queue() (map[string]interface{}, error) {
	return c.QueueContext(context.Background())
}

func (c *Client) QueueContext(ctx context.Context) (map[string]interface{}, error) {
	var result map[string]interface{}
	response, err := c.do(ctx, http.MethodGet, "/queue", nil, "queue")
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if err := decodeBounded(response.Body, c.maxJSONBytes(), &result, "queue response"); err != nil {
		return nil, err
	}
	return result, nil
}

func (c *Client) DeleteFromQueue(promptIDs []string) error {
	return c.DeleteFromQueueContext(context.Background(), promptIDs)
}

func (c *Client) DeleteFromQueueContext(ctx context.Context, promptIDs []string) error {
	body, err := json.Marshal(map[string]interface{}{"delete": promptIDs})
	if err != nil {
		return fmt.Errorf("encode queue deletion: %w", err)
	}
	response, err := c.doJSON(ctx, http.MethodPost, "/queue", body, "queue delete")
	if err != nil {
		return err
	}
	return response.Body.Close()
}

func (c *Client) View(filename, subfolder, typ string) ([]byte, error) {
	body, _, err := c.Fetch(context.Background(), output.Descriptor{Filename: filename, Subfolder: subfolder, Type: typ}, 0)
	if err != nil {
		return nil, err
	}
	defer body.Close()
	return readBounded(body, c.maxOutputBytes(), "output response")
}

func (c *Client) Fetch(ctx context.Context, descriptor output.Descriptor, offset int64) (io.ReadCloser, output.FetchInfo, error) {
	query := url.Values{}
	query.Set("filename", descriptor.Filename)
	if descriptor.Subfolder != "" {
		query.Set("subfolder", descriptor.Subfolder)
	}
	if descriptor.Type != "" {
		query.Set("type", descriptor.Type)
	}
	request, err := c.request(ctx, http.MethodGet, "/view?"+query.Encode(), nil)
	if err != nil {
		return nil, output.FetchInfo{}, err
	}
	if offset > 0 {
		request.Header.Set("Range", fmt.Sprintf("bytes=%d-", offset))
	}
	response, err := c.HTTP.Do(request)
	if err != nil {
		return nil, output.FetchInfo{}, err
	}
	if response.StatusCode != http.StatusOK && response.StatusCode != http.StatusPartialContent {
		err := c.responseError("view", response)
		response.Body.Close()
		return nil, output.FetchInfo{}, err
	}
	return response.Body, output.FetchInfo{Partial: response.StatusCode == http.StatusPartialContent}, nil
}

func (c *Client) ProbeAssetContext(ctx context.Context, filename, typ string) error {
	query := url.Values{}
	query.Set("filename", filename)
	if typ != "" {
		query.Set("type", typ)
	}
	request, err := c.request(ctx, http.MethodGet, "/view?"+query.Encode(), nil)
	if err != nil {
		return err
	}
	request.Header.Set("Range", "bytes=0-0")
	response, err := c.HTTP.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK && response.StatusCode != http.StatusPartialContent {
		return c.responseError("probe asset", response)
	}
	return nil
}

func (c *Client) ModelFoldersContext(ctx context.Context) ([]string, error) {
	return c.stringListContext(ctx, "/models", "model folders")
}

func (c *Client) ModelsContext(ctx context.Context, folder string) ([]string, error) {
	folder = strings.TrimSpace(folder)
	if folder == "" || strings.Contains(folder, "/") || strings.Contains(folder, `\\`) || folder == "." || folder == ".." {
		return nil, errors.New("invalid model folder")
	}
	return c.stringListContext(ctx, "/models/"+url.PathEscape(folder), "models")
}

func (c *Client) stringListContext(ctx context.Context, endpoint, operation string) ([]string, error) {
	var result []string
	response, err := c.do(ctx, http.MethodGet, endpoint, nil, operation)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if err := decodeBounded(response.Body, c.maxJSONBytes(), &result, operation+" response"); err != nil {
		return nil, err
	}
	if len(result) > 100_000 {
		return nil, fmt.Errorf("%s count exceeds limit", operation)
	}
	return result, nil
}

func (c *Client) ObjectInfoContext(ctx context.Context) (map[string]interface{}, error) {
	var result map[string]interface{}
	response, err := c.do(ctx, http.MethodGet, "/object_info", nil, "object info")
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if err := decodeBounded(response.Body, c.maxJSONBytes(), &result, "object info response"); err != nil {
		return nil, err
	}
	return result, nil
}

func (c *Client) doJSON(ctx context.Context, method, endpoint string, body []byte, operation string) (*http.Response, error) {
	request, err := c.request(ctx, method, endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	request.Header.Set("Content-Type", "application/json")
	return c.execute(request, operation)
}

func (c *Client) do(ctx context.Context, method, endpoint string, body io.Reader, operation string) (*http.Response, error) {
	request, err := c.request(ctx, method, endpoint, body)
	if err != nil {
		return nil, err
	}
	return c.execute(request, operation)
}

func (c *Client) request(ctx context.Context, method, endpoint string, body io.Reader) (*http.Request, error) {
	if c.initErr != nil {
		return nil, c.initErr
	}
	if c.base == nil || c.HTTP == nil {
		return nil, errors.New("ComfyUI client is not initialized")
	}
	return http.NewRequestWithContext(ctx, method, c.BaseURL+endpoint, body)
}

func (c *Client) execute(request *http.Request, operation string) (*http.Response, error) {
	response, err := c.HTTP.Do(request)
	if err != nil {
		return nil, err
	}
	if response.StatusCode >= 400 {
		err := c.responseError(operation, response)
		response.Body.Close()
		return nil, err
	}
	return response, nil
}

func (c *Client) responseError(operation string, response *http.Response) error {
	body, _ := readBounded(response.Body, c.maxErrorBytes(), "server error")
	return &responseError{operation: operation, status: response.StatusCode, detail: strings.TrimSpace(string(body))}
}

func decodeBounded(reader io.Reader, limit int64, destination any, label string) error {
	body, err := readBounded(reader, limit, label)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(body, destination); err != nil {
		return fmt.Errorf("decode %s: %w", label, err)
	}
	return nil
}

func readBounded(reader io.Reader, limit int64, label string) ([]byte, error) {
	if limit <= 0 {
		return nil, fmt.Errorf("%s byte limit is invalid", label)
	}
	body, err := io.ReadAll(io.LimitReader(reader, limit+1))
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", label, err)
	}
	if int64(len(body)) > limit {
		return nil, fmt.Errorf("%s exceeds byte limit of %d", label, limit)
	}
	return body, nil
}

func (c *Client) maxJSONBytes() int64 {
	if c.options.MaxJSONBytes > 0 {
		return c.options.MaxJSONBytes
	}
	return defaultMaxJSONBytes
}

func (c *Client) maxErrorBytes() int64 {
	if c.options.MaxErrorBytes > 0 {
		return c.options.MaxErrorBytes
	}
	return defaultMaxErrorBytes
}

func (c *Client) maxUploadBytes() int64 {
	if c.options.MaxUploadBytes > 0 {
		return c.options.MaxUploadBytes
	}
	return defaultMaxUploadBytes
}

func (c *Client) maxOutputBytes() int64 {
	if c.options.MaxOutputBytes > 0 {
		return c.options.MaxOutputBytes
	}
	return defaultMaxOutputBytes
}
