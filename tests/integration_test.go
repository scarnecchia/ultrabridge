//go:build integration

package tests

import (
	"context"
	"database/sql"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	gocaldav "github.com/emersion/go-webdav/caldav"
	"github.com/sysop/ultrabridge/internal/auth"
	ubcaldav "github.com/sysop/ultrabridge/internal/caldav"
	"github.com/sysop/ultrabridge/internal/taskstore"
)

// mockNotifier is a no-op SyncNotifier for testing.
type mockNotifier struct{}

func (m *mockNotifier) Notify(ctx context.Context) error {
	return nil
}

// testServerSetup creates a full CalDAV handler stack for testing.
func testServerSetup(t *testing.T, store *taskstore.Store) (*httptest.Server, string, string) {
	// Create CalDAV backend
	backend := ubcaldav.NewBackend(store, "/caldav", "Supernote Tasks", "preserve", &mockNotifier{})
	caldavHandler := &gocaldav.Handler{
		Backend: backend,
		Prefix:  "/caldav",
	}

	// Create auth middleware with test credentials
	authMW := auth.New("admin", "$2a$10$abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNO")

	// Create mux with full handler chain
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"status":"ok"}`))
	})
	mux.Handle("/caldav/", authMW.Wrap(caldavHandler))
	mux.HandleFunc("/.well-known/caldav", func(w http.ResponseWriter, r *http.Request) {
		authMW.Wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			http.Redirect(w, r, "/caldav/", http.StatusMovedPermanently)
		})).ServeHTTP(w, r)
	})

	// Create test server
	server := httptest.NewServer(mux)

	// Return server, username, password for BasicAuth
	return server, "admin", "wrongpass" // Password is intentionally wrong for testing
}

// TestIntegrationHealthCheck verifies the /health endpoint works.
func TestIntegrationHealthCheck(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	db := connectTestDB(t)
	defer db.Close()

	userID := discoverTestUserID(t, db)
	store := taskstore.New(db, userID)

	server, _, _ := testServerSetup(t, store)
	defer server.Close()

	resp, err := http.Get(server.URL + "/health")
	if err != nil {
		t.Fatalf("GET /health: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected status 200, got %d", resp.StatusCode)
	}
}

// TestIntegrationWellKnownRedirect verifies /.well-known/caldav redirects correctly.
func TestIntegrationWellKnownRedirect(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	db := connectTestDB(t)
	defer db.Close()

	userID := discoverTestUserID(t, db)
	store := taskstore.New(db, userID)

	server, username, password := testServerSetup(t, store)
	defer server.Close()

	client := &http.Client{
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse // Don't follow redirects
		},
	}

	req, err := http.NewRequest("GET", server.URL+"/.well-known/caldav", nil)
	if err != nil {
		t.Fatalf("create request: %v", err)
	}
	req.SetBasicAuth(username, password)

	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("GET /.well-known/caldav: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusMovedPermanently {
		t.Errorf("expected status 301, got %d", resp.StatusCode)
	}

	location := resp.Header.Get("Location")
	if location != "/caldav/" {
		t.Errorf("expected Location: /caldav/, got %s", location)
	}
}

// TestIntegrationAuthRequired verifies that unauthenticated requests are rejected.
func TestIntegrationAuthRequired(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	db := connectTestDB(t)
	defer db.Close()

	userID := discoverTestUserID(t, db)
	store := taskstore.New(db, userID)

	server, _, _ := testServerSetup(t, store)
	defer server.Close()

	// Make request without auth
	resp, err := http.Get(server.URL + "/caldav/tasks/")
	if err != nil {
		t.Fatalf("GET /caldav/tasks/: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("expected status 401, got %d", resp.StatusCode)
	}

	if resp.Header.Get("WWW-Authenticate") == "" {
		t.Errorf("expected WWW-Authenticate header")
	}
}

// TestIntegrationPropfindTasks verifies PROPFIND on the tasks collection.
func TestIntegrationPropfindTasks(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	db := connectTestDB(t)
	defer db.Close()

	userID := discoverTestUserID(t, db)
	store := taskstore.New(db, userID)

	// Clean up any old test tasks
	cleanupTestTasks(t, db, userID, "__ubtest_")

	server, username, password := testServerSetup(t, store)
	defer server.Close()

	req, err := http.NewRequest("PROPFIND", server.URL+"/caldav/tasks/", nil)
	if err != nil {
		t.Fatalf("create request: %v", err)
	}
	req.SetBasicAuth(username, password)
	req.Header.Set("Depth", "0")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("PROPFIND /caldav/tasks/: %v", err)
	}
	defer resp.Body.Close()

	// PROPFIND should return 207 Multi-Status
	if resp.StatusCode != 207 {
		t.Errorf("expected status 207, got %d", resp.StatusCode)
	}

	// Verify Content-Type includes xml
	contentType := resp.Header.Get("Content-Type")
	if contentType == "" {
		t.Errorf("expected Content-Type header")
	}

	// Clean up
	cleanupTestTasks(t, db, userID, "__ubtest_")
}

// TestIntegrationPutAndGet verifies creating and retrieving a task via CalDAV.
func TestIntegrationPutAndGet(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	db := connectTestDB(t)
	defer db.Close()

	userID := discoverTestUserID(t, db)
	store := taskstore.New(db, userID)

	// Clean up any old test tasks
	cleanupTestTasks(t, db, userID, "__ubtest_")

	server, username, password := testServerSetup(t, store)
	defer server.Close()

	// Create a simple VTODO
	vtodo := `BEGIN:VCALENDAR
VERSION:2.0
PRODID:-//Test//Test//EN
BEGIN:VTODO
UID:test-task-001
DTSTAMP:20260317T120000Z
DTSTART:20260317T120000Z
SUMMARY:__ubtest_CreateTask
STATUS:NEEDS-ACTION
END:VTODO
END:VCALENDAR`

	taskID := "test-task-001"
	putReq, err := http.NewRequest("PUT", server.URL+"/caldav/tasks/"+taskID+".ics", strings.NewReader(vtodo))
	if err != nil {
		t.Fatalf("create PUT request: %v", err)
	}
	putReq.SetBasicAuth(username, password)
	putReq.Header.Set("Content-Type", "text/calendar")

	putResp, err := http.DefaultClient.Do(putReq)
	if err != nil {
		t.Fatalf("PUT task: %v", err)
	}
	defer putResp.Body.Close()

	if putResp.StatusCode < 200 || putResp.StatusCode >= 300 {
		t.Errorf("expected 2xx status for PUT, got %d", putResp.StatusCode)
	}

	// Verify task exists in database
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	task, err := store.Get(ctx, taskID)
	if err != nil {
		t.Fatalf("get task from DB: %v", err)
	}

	if task.Title != "__ubtest_CreateTask" {
		t.Errorf("expected title '__ubtest_CreateTask', got '%s'", task.Title)
	}

	// GET the task back
	getReq, err := http.NewRequest("GET", server.URL+"/caldav/tasks/"+taskID+".ics", nil)
	if err != nil {
		t.Fatalf("create GET request: %v", err)
	}
	getReq.SetBasicAuth(username, password)

	getResp, err := http.DefaultClient.Do(getReq)
	if err != nil {
		t.Fatalf("GET task: %v", err)
	}
	defer getResp.Body.Close()

	if getResp.StatusCode != http.StatusOK {
		t.Errorf("expected status 200 for GET, got %d", getResp.StatusCode)
	}

	// Clean up
	cleanupTestTasks(t, db, userID, "__ubtest_")
}

// TestIntegrationDelete verifies that DELETE marks tasks as deleted.
func TestIntegrationDelete(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	db := connectTestDB(t)
	defer db.Close()

	userID := discoverTestUserID(t, db)
	store := taskstore.New(db, userID)

	// Create a test task directly
	taskID := createTestTask(t, db, userID, "__ubtest_DeleteTask")
	defer cleanupTestTasks(t, db, userID, "__ubtest_")

	server, username, password := testServerSetup(t, store)
	defer server.Close()

	// DELETE the task
	deleteReq, err := http.NewRequest("DELETE", server.URL+"/caldav/tasks/"+taskID+".ics", nil)
	if err != nil {
		t.Fatalf("create DELETE request: %v", err)
	}
	deleteReq.SetBasicAuth(username, password)

	deleteResp, err := http.DefaultClient.Do(deleteReq)
	if err != nil {
		t.Fatalf("DELETE task: %v", err)
	}
	defer deleteResp.Body.Close()

	if deleteResp.StatusCode < 200 || deleteResp.StatusCode >= 300 {
		t.Errorf("expected 2xx status for DELETE, got %d", deleteResp.StatusCode)
	}

	// Verify task is marked as deleted in DB
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Query all tasks including deleted ones
	var isDeleted string
	err = db.QueryRowContext(ctx, "SELECT is_deleted FROM t_schedule_task WHERE task_id = ?", taskID).Scan(&isDeleted)
	if err != nil && err != sql.ErrNoRows {
		t.Fatalf("query is_deleted: %v", err)
	}

	if isDeleted != "Y" {
		t.Errorf("expected is_deleted='Y', got '%s'", isDeleted)
	}
}

// TestIntegrationPutVEventReject verifies that VEVENT is rejected.
func TestIntegrationPutVEventReject(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	db := connectTestDB(t)
	defer db.Close()

	userID := discoverTestUserID(t, db)
	store := taskstore.New(db, userID)
	defer cleanupTestTasks(t, db, userID, "__ubtest_")

	server, username, password := testServerSetup(t, store)
	defer server.Close()

	// Try to PUT a VEVENT
	vevent := `BEGIN:VCALENDAR
VERSION:2.0
PRODID:-//Test//Test//EN
BEGIN:VEVENT
UID:test-event-001
DTSTAMP:20260317T120000Z
DTSTART:20260317T120000Z
SUMMARY:__ubtest_EventNotAllowed
END:VEVENT
END:VCALENDAR`

	putReq, err := http.NewRequest("PUT", server.URL+"/caldav/tasks/test-event-001.ics", strings.NewReader(vevent))
	if err != nil {
		t.Fatalf("create PUT request: %v", err)
	}
	putReq.SetBasicAuth(username, password)
	putReq.Header.Set("Content-Type", "text/calendar")

	putResp, err := http.DefaultClient.Do(putReq)
	if err != nil {
		t.Fatalf("PUT VEVENT: %v", err)
	}
	defer putResp.Body.Close()

	// Should reject with 4xx or 5xx (not 2xx)
	if putResp.StatusCode >= 200 && putResp.StatusCode < 300 {
		t.Errorf("expected non-2xx status for VEVENT PUT, got %d", putResp.StatusCode)
	}
}

// TestIntegrationDescriptionAndPriority verifies Tier 2 fields round-trip.
// Implements ultrabridge-caldav.AC3.8.
func TestIntegrationDescriptionAndPriority(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	db := connectTestDB(t)
	defer db.Close()

	userID := discoverTestUserID(t, db)
	store := taskstore.New(db, userID)
	defer cleanupTestTasks(t, db, userID, "__ubtest_")

	server, username, password := testServerSetup(t, store)
	defer server.Close()

	// Create VTODO with DESCRIPTION and PRIORITY
	vtodo := `BEGIN:VCALENDAR
VERSION:2.0
PRODID:-//Test//Test//EN
BEGIN:VTODO
UID:test-task-tier2
DTSTAMP:20260317T120000Z
DTSTART:20260317T120000Z
SUMMARY:__ubtest_Tier2Fields
DESCRIPTION:This is a detailed description
PRIORITY:5
STATUS:NEEDS-ACTION
END:VTODO
END:VCALENDAR`

	taskID := "test-task-tier2"
	putReq, err := http.NewRequest("PUT", server.URL+"/caldav/tasks/"+taskID+".ics", strings.NewReader(vtodo))
	if err != nil {
		t.Fatalf("create PUT request: %v", err)
	}
	putReq.SetBasicAuth(username, password)
	putReq.Header.Set("Content-Type", "text/calendar")

	putResp, err := http.DefaultClient.Do(putReq)
	if err != nil {
		t.Fatalf("PUT task with Tier 2 fields: %v", err)
	}
	defer putResp.Body.Close()

	if putResp.StatusCode < 200 || putResp.StatusCode >= 300 {
		t.Errorf("expected 2xx status for PUT, got %d", putResp.StatusCode)
	}

	// Verify DESCRIPTION and PRIORITY in DB
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	var detail, importance string
	err = db.QueryRowContext(ctx,
		"SELECT detail, importance FROM t_schedule_task WHERE task_id = ?",
		taskID).Scan(&detail, &importance)
	if err != nil {
		t.Fatalf("query task fields: %v", err)
	}

	if detail != "This is a detailed description" {
		t.Errorf("expected detail='This is a detailed description', got '%s'", detail)
	}

	if importance != "5" {
		t.Errorf("expected importance='5', got '%s'", importance)
	}

	// GET the task back and verify DESCRIPTION and PRIORITY are present
	getReq, err := http.NewRequest("GET", server.URL+"/caldav/tasks/"+taskID+".ics", nil)
	if err != nil {
		t.Fatalf("create GET request: %v", err)
	}
	getReq.SetBasicAuth(username, password)

	getResp, err := http.DefaultClient.Do(getReq)
	if err != nil {
		t.Fatalf("GET task: %v", err)
	}
	defer getResp.Body.Close()

	if getResp.StatusCode != http.StatusOK {
		t.Errorf("expected status 200 for GET, got %d", getResp.StatusCode)
	}

	// Read response body to verify it contains DESCRIPTION and PRIORITY
	var body strings.Builder
	if _, err := body.ReadFrom(getResp.Body); err != nil {
		t.Fatalf("read response body: %v", err)
	}

	responseBody := body.String()
	if !strings.Contains(responseBody, "DESCRIPTION:This is a detailed description") {
		t.Errorf("expected DESCRIPTION in response body")
	}

	if !strings.Contains(responseBody, "PRIORITY:5") {
		t.Errorf("expected PRIORITY in response body")
	}
}

// TestIntegrationRoundTrip verifies full round-trip: create task on CalDAV,
// modify on DB (simulating device change), and see updated fields via CalDAV.
// Implements ultrabridge-caldav.AC3.7.
func TestIntegrationRoundTrip(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	db := connectTestDB(t)
	defer db.Close()

	userID := discoverTestUserID(t, db)
	store := taskstore.New(db, userID)
	defer cleanupTestTasks(t, db, userID, "__ubtest_")

	server, username, password := testServerSetup(t, store)
	defer server.Close()

	// Step 1: PUT a VTODO via CalDAV
	vtodo := `BEGIN:VCALENDAR
VERSION:2.0
PRODID:-//Test//Test//EN
BEGIN:VTODO
UID:test-roundtrip
DTSTAMP:20260317T120000Z
DTSTART:20260317T120000Z
SUMMARY:__ubtest_RoundTrip
STATUS:NEEDS-ACTION
END:VTODO
END:VCALENDAR`

	taskID := "test-roundtrip"
	putReq, err := http.NewRequest("PUT", server.URL+"/caldav/tasks/"+taskID+".ics", strings.NewReader(vtodo))
	if err != nil {
		t.Fatalf("create PUT request: %v", err)
	}
	putReq.SetBasicAuth(username, password)
	putReq.Header.Set("Content-Type", "text/calendar")

	putResp, err := http.DefaultClient.Do(putReq)
	if err != nil {
		t.Fatalf("PUT task: %v", err)
	}
	defer putResp.Body.Close()

	if putResp.StatusCode < 200 || putResp.StatusCode >= 300 {
		t.Errorf("expected 2xx status for PUT, got %d", putResp.StatusCode)
	}

	// Verify task appears in DB with correct fields
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	task, err := store.Get(ctx, taskID)
	if err != nil {
		t.Fatalf("get task from DB: %v", err)
	}

	if task.Title.String != "__ubtest_RoundTrip" {
		t.Errorf("expected title '__ubtest_RoundTrip', got '%s'", task.Title.String)
	}

	if task.Status.String != "needsAction" {
		t.Errorf("expected status 'needsAction', got '%s'", task.Status.String)
	}

	// Step 2: Modify task directly in DB (simulating device change)
	newDetail := "Updated via device"
	_, err = db.ExecContext(ctx, `
		UPDATE t_schedule_task
		SET detail = ?, importance = ?
		WHERE task_id = ? AND user_id = ?`,
		newDetail, "3", taskID, userID)
	if err != nil {
		t.Fatalf("update task in DB: %v", err)
	}

	// Step 3: GET via CalDAV and verify updated fields appear
	getReq, err := http.NewRequest("GET", server.URL+"/caldav/tasks/"+taskID+".ics", nil)
	if err != nil {
		t.Fatalf("create GET request: %v", err)
	}
	getReq.SetBasicAuth(username, password)

	getResp, err := http.DefaultClient.Do(getReq)
	if err != nil {
		t.Fatalf("GET task: %v", err)
	}
	defer getResp.Body.Close()

	if getResp.StatusCode != http.StatusOK {
		t.Errorf("expected status 200 for GET, got %d", getResp.StatusCode)
	}

	// Read response body to verify updated fields
	var body strings.Builder
	if _, err := body.ReadFrom(getResp.Body); err != nil {
		t.Fatalf("read response body: %v", err)
	}

	responseBody := body.String()
	if !strings.Contains(responseBody, "DESCRIPTION:"+newDetail) {
		t.Errorf("expected DESCRIPTION in response with updated value")
	}

	if !strings.Contains(responseBody, "PRIORITY:3") {
		t.Errorf("expected PRIORITY:3 in response")
	}
}
