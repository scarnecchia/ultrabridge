package supernote

// pattern: Imperative Shell

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"sync"
	"time"
)

// Client is an HTTP client for the Supernote Private Cloud REST API.
type Client struct {
	apiURL   string
	password string
	logger   *slog.Logger
	client   http.Client

	mu    sync.Mutex
	token string
}

// NewClient creates a new SPC REST API client.
func NewClient(apiURL, password string, logger *slog.Logger) *Client {
	return &Client{
		apiURL:   apiURL,
		password: password,
		logger:   logger,
		client:   http.Client{Timeout: 30 * time.Second},
	}
}

// Login performs the challenge-response JWT authentication flow.
func (c *Client) Login(ctx context.Context) error {
	// Step 1: Get random code
	var codeResp struct {
		Success    bool   `json:"success"`
		RandomCode string `json:"randomCode"`
		Timestamp  int64  `json:"timestamp"`
	}
	if err := c.postJSON(ctx, "/api/official/user/query/random/code", nil, &codeResp, false); err != nil {
		return fmt.Errorf("get random code: %w", err)
	}

	// Step 2: Hash password with challenge
	hash := sha256.Sum256([]byte(c.password + codeResp.RandomCode))
	hashedPW := fmt.Sprintf("%x", hash)

	// Step 3: Login
	loginBody := map[string]any{
		"password":   hashedPW,
		"randomCode": codeResp.RandomCode,
		"timestamp":  codeResp.Timestamp,
	}
	var loginResp struct {
		Success bool   `json:"success"`
		Token   string `json:"token"`
	}
	if err := c.postJSON(ctx, "/api/official/user/account/login/equipment", loginBody, &loginResp, false); err != nil {
		return fmt.Errorf("login: %w", err)
	}

	c.mu.Lock()
	c.token = loginResp.Token
	c.mu.Unlock()

	c.logger.Info("SPC login successful")
	return nil
}

// FetchTasks returns all tasks from all groups in SPC.
func (c *Client) FetchTasks(ctx context.Context) ([]SPCTask, error) {
	// Fetch groups first
	var groupsResp struct {
		Success bool `json:"success"`
		Data    []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := c.getJSON(ctx, "/api/file/schedule/task/group/list", &groupsResp); err != nil {
		return nil, fmt.Errorf("fetch groups: %w", err)
	}

	// Fetch tasks from each group
	var allTasks []SPCTask
	for _, group := range groupsResp.Data {
		var tasksResp struct {
			Success bool      `json:"success"`
			Data    []SPCTask `json:"data"`
		}
		url := fmt.Sprintf("/api/file/schedule/task/list?groupId=%s", group.ID)
		if err := c.getJSON(ctx, url, &tasksResp); err != nil {
			c.logger.Warn("fetch tasks for group failed", "group_id", group.ID, "error", err)
			continue
		}
		allTasks = append(allTasks, tasksResp.Data...)
	}

	return allTasks, nil
}

// CreateTask creates a single task on SPC.
func (c *Client) CreateTask(ctx context.Context, task SPCTask) error {
	var resp struct{ Success bool `json:"success"` }
	return c.postJSON(ctx, "/api/file/schedule/task/create", task, &resp, true)
}

// UpdateTasks performs a bulk update of tasks on SPC.
func (c *Client) UpdateTasks(ctx context.Context, tasks []SPCTask) error {
	var resp struct{ Success bool `json:"success"` }
	return c.postJSON(ctx, "/api/file/schedule/task/update", tasks, &resp, true)
}

// DeleteTask deletes a task on SPC.
func (c *Client) DeleteTask(ctx context.Context, taskID string) error {
	body := map[string]string{"id": taskID}
	var resp struct{ Success bool `json:"success"` }
	return c.postJSON(ctx, "/api/file/schedule/task/delete", body, &resp, true)
}

func (c *Client) postJSON(ctx context.Context, path string, body any, result any, auth bool) error {
	var bodyReader io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("marshal body: %w", err)
		}
		bodyReader = bytes.NewReader(data)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", c.apiURL+path, bodyReader)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	if auth {
		c.mu.Lock()
		token := c.token
		c.mu.Unlock()
		req.Header.Set("x-access-token", token)
	}

	resp, err := c.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusUnauthorized && auth {
		// Re-authenticate and retry once
		if err := c.Login(ctx); err != nil {
			return fmt.Errorf("re-auth failed: %w", err)
		}
		return c.postJSON(ctx, path, body, result, false) // false to prevent infinite loop
	}

	if resp.StatusCode != http.StatusOK {
		errBody, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return fmt.Errorf("SPC %s returned %d: %s", path, resp.StatusCode, errBody)
	}

	if result != nil {
		return json.NewDecoder(resp.Body).Decode(result)
	}
	return nil
}

func (c *Client) getJSON(ctx context.Context, path string, result any) error {
	return c.doGetJSON(ctx, path, result, false)
}

func (c *Client) doGetJSON(ctx context.Context, path string, result any, retried bool) error {
	req, err := http.NewRequestWithContext(ctx, "GET", c.apiURL+path, nil)
	if err != nil {
		return err
	}
	c.mu.Lock()
	token := c.token
	c.mu.Unlock()
	req.Header.Set("x-access-token", token)

	resp, err := c.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusUnauthorized && !retried {
		if err := c.Login(ctx); err != nil {
			return fmt.Errorf("re-auth failed: %w", err)
		}
		return c.doGetJSON(ctx, path, result, true)
	}

	if resp.StatusCode != http.StatusOK {
		errBody, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return fmt.Errorf("SPC %s returned %d: %s", path, resp.StatusCode, errBody)
	}

	return json.NewDecoder(resp.Body).Decode(result)
}

// SPCTask is the wire format for tasks in the SPC REST API.
type SPCTask struct {
	ID            string `json:"id"`
	TaskListID    string `json:"taskListId,omitempty"`
	Title         string `json:"title"`
	Detail        string `json:"detail,omitempty"`
	Status        string `json:"status"`
	Importance    string `json:"importance,omitempty"`
	DueTime       int64  `json:"dueTime"`
	CompletedTime int64  `json:"completedTime"` // Supernote quirk: holds creation time
	LastModified  int64  `json:"lastModified"`  // Supernote quirk: holds completion time when completed
	Recurrence    string `json:"recurrence,omitempty"`
	IsReminderOn  string `json:"isReminderOn"`
	Links         string `json:"links,omitempty"`
	IsDeleted     string `json:"isDeleted"`
}
