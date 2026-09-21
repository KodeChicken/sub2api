package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/gin-gonic/gin"
)

func imageSessionTestRouter(h *ImageSessionHandler, userID int64) *gin.Engine {
	r := gin.New()
	r.Use(func(c *gin.Context) {
		c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: userID})
	})
	r.PUT("/sessions", h.Save)
	r.GET("/sessions/records", h.ListRecords)
	r.GET("/sessions/assets/:asset", h.ReadAsset)
	r.POST("/sessions/:id/assets", h.UploadAsset)
	return r
}

func TestImageSessionUploadRejectsNonImage(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	h := NewImageSessionHandler(db, &config.Config{})
	id := "f23e3aeb-e1db-4cc8-a34e-761107d2cf6f"
	mock.ExpectQuery("SELECT EXISTS").WithArgs(id, int64(42)).WillReturnRows(sqlmock.NewRows([]string{"exists"}).AddRow(true))
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, err := writer.CreateFormFile("file", "image.png")
	if err != nil {
		t.Fatal(err)
	}
	_, _ = part.Write([]byte("not an image"))
	_ = writer.Close()
	request := httptest.NewRequest(http.MethodPost, "/sessions/"+id+"/assets", &body)
	request.Header.Set("Content-Type", writer.FormDataContentType())
	w := httptest.NewRecorder()
	imageSessionTestRouter(h, 42).ServeHTTP(w, request)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestImageSessionCompleteTaskPersistsLocalImage(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	root := t.TempDir()
	h := NewImageSessionHandler(db, &config.Config{})
	h.imageStorageRoot = root
	h.sessionRoot = t.TempDir()
	userID := int64(42)
	sessionID := "f23e3aeb-e1db-4cc8-a34e-761107d2cf6f"
	recordID := "e23e3aeb-e1db-4cc8-a34e-761107d2cf6f"
	file := filepath.Join(root, "images", "result.png")
	if err := os.MkdirAll(filepath.Dir(file), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, append([]byte("\x89PNG\r\n\x1a\n"), make([]byte, 32)...), 0600); err != nil {
		t.Fatal(err)
	}
	payload := fmt.Sprintf(`{"id":%q,"sessionId":%q,"taskId":"task-1","createdAt":1000,"status":"processing"}`, recordID, sessionID)
	mock.ExpectQuery("SELECT id,session_id,user_id,payload").WithArgs("task-1").WillReturnRows(sqlmock.NewRows([]string{"id", "session_id", "user_id", "payload"}).AddRow(recordID, sessionID, userID, []byte(payload)))
	mock.ExpectExec("INSERT INTO image_generation_assets").WithArgs(sqlmock.AnyArg(), sessionID, userID, sqlmock.AnyArg(), "image/png", int64(40)).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("UPDATE image_generation_records SET payload").WithArgs(sqlmock.AnyArg(), recordID, userID).WillReturnResult(sqlmock.NewResult(0, 1))
	if err := h.CompleteTask(context.Background(), "task-1", json.RawMessage(`{"data":[{"url":"/v1/images/storage/images/result.png"}]}`)); err != nil {
		t.Fatal(err)
	}
	files, err := filepath.Glob(filepath.Join(h.sessionRoot, "42", sessionID, "*.png"))
	if err != nil || len(files) != 1 {
		t.Fatalf("stored files = %v, err = %v", files, err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestImageSessionCannotOverwriteAnotherUser(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	h := NewImageSessionHandler(db, &config.Config{})
	mock.ExpectExec("INSERT INTO image_generation_sessions").WithArgs(
		"f23e3aeb-e1db-4cc8-a34e-761107d2cf6f", int64(42), "Private", int64(1), int64(1000), int64(1000),
	).WillReturnResult(sqlmock.NewResult(0, 0))
	request := httptest.NewRequest(http.MethodPut, "/sessions", strings.NewReader(`{"id":"f23e3aeb-e1db-4cc8-a34e-761107d2cf6f","title":"Private","sortOrder":1,"createdAt":1000,"updatedAt":1000}`))
	request.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	imageSessionTestRouter(h, 42).ServeHTTP(w, request)
	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestImageSessionAssetsAreScopedToUser(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	h := NewImageSessionHandler(db, &config.Config{})
	mock.ExpectQuery("SELECT path,mime_type FROM image_generation_assets").WithArgs("f23e3aeb-e1db-4cc8-a34e-761107d2cf6f", int64(42)).WillReturnRows(sqlmock.NewRows([]string{"path", "mime_type"}))
	w := httptest.NewRecorder()
	imageSessionTestRouter(h, 42).ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/sessions/assets/f23e3aeb-e1db-4cc8-a34e-761107d2cf6f", nil))
	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d", w.Code)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}
