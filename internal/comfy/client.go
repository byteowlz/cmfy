package comfy

import (
	"bytes"
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
)

type Client struct {
	BaseURL string
	HTTP    *http.Client
}

func NewClient(baseURL string) *Client {
	c := &Client{BaseURL: strings.TrimRight(baseURL, "/"), HTTP: &http.Client{Timeout: 60 * time.Second}}
	return c
}

func (c *Client) Ping() error {
	u := c.BaseURL + "/system_stats"
	req, _ := http.NewRequest(http.MethodGet, u, nil)
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return fmt.Errorf("server responded %d", resp.StatusCode)
	}
	return nil
}

// Upload uploads a file as ComfyUI input. Returns server-side filename.
func (c *Client) Upload(filePath string) (string, error) {
	f, err := os.Open(filePath)
	if err != nil {
		return "", err
	}
	defer f.Close()

	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	// type=input so the image can be referenced by LoadImage etc.
	_ = mw.WriteField("type", "input")
	fw, err := mw.CreateFormFile("image", path.Base(filePath))
	if err != nil {
		return "", err
	}
	if _, err := io.Copy(fw, f); err != nil {
		return "", err
	}
	if err := mw.Close(); err != nil {
		return "", err
	}

	u := c.BaseURL + "/upload/image"
	req, _ := http.NewRequest(http.MethodPost, u, &buf)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		_, _ = io.ReadAll(resp.Body)
		return "", fmt.Errorf("upload failed: %d: %s", resp.StatusCode, resp.Status)
	}
	var res struct {
		Name string `json:"name"`
	}
	// Some servers return a JSON array of names; handle both.
	b, _ := io.ReadAll(resp.Body)
	if len(b) == 0 {
		return path.Base(filePath), nil
	}
	if b[0] == '[' {
		var arr []string
		if err := json.Unmarshal(b, &arr); err == nil && len(arr) > 0 {
			return arr[0], nil
		}
		return path.Base(filePath), nil
	}
	_ = json.Unmarshal(b, &res)
	if res.Name != "" {
		return res.Name, nil
	}
	return path.Base(filePath), nil
}

// Prompt submits a prompt graph. Returns prompt_id.
func (c *Client) Prompt(clientID string, prompt map[string]interface{}) (string, error) {
	body := map[string]interface{}{
		"client_id": clientID,
		"prompt":    prompt,
	}
	b, _ := json.Marshal(body)
	u := c.BaseURL + "/prompt"
	req, _ := http.NewRequest(http.MethodPost, u, bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		rb, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("prompt submit failed: %s", string(rb))
	}
	var res struct {
		PromptID string `json:"prompt_id"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&res); err != nil {
		return "", err
	}
	if res.PromptID == "" {
		return "", errors.New("empty prompt_id")
	}
	return res.PromptID, nil
}

// History retrieves a prompt execution history entry.
func (c *Client) History(promptID string) (map[string]interface{}, error) {
	u := c.BaseURL + "/history/" + url.PathEscape(promptID)
	req, _ := http.NewRequest(http.MethodGet, u, nil)
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		b, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("history failed: %s", string(b))
	}
	var res map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&res); err != nil {
		return nil, err
	}
	return res, nil
}

// View fetches an output asset by filename/subfolder/type.
func (c *Client) View(filename, subfolder, typ string) ([]byte, error) {
	q := url.Values{}
	q.Set("filename", filename)
	if subfolder != "" {
		q.Set("subfolder", subfolder)
	}
	if typ != "" {
		q.Set("type", typ)
	}
	u := c.BaseURL + "/view?" + q.Encode()
	req, _ := http.NewRequest(http.MethodGet, u, nil)
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		b, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("view failed: %s", string(b))
	}
	return io.ReadAll(resp.Body)
}
