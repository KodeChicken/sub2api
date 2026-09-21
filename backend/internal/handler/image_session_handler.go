package handler

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

const imageSessionRoot = "./data/image-sessions"
const maxSessionAssetBytes = 25 << 20

type ImageSessionHandler struct {
	db               *sql.DB
	imageStorageRoot string
	sessionRoot      string
}

func NewImageSessionHandler(db *sql.DB, cfg *config.Config) *ImageSessionHandler {
	root := cfg.ImageStorage.LocalDir
	if strings.TrimSpace(root) == "" {
		root = "./data/image-storage"
	}
	return &ImageSessionHandler{db: db, imageStorageRoot: root, sessionRoot: imageSessionRoot}
}

type imageSessionInput struct {
	ID        string          `json:"id"`
	Title     string          `json:"title"`
	SortOrder int64           `json:"sortOrder"`
	CreatedAt int64           `json:"createdAt"`
	UpdatedAt int64           `json:"updatedAt"`
	Draft     json.RawMessage `json:"draft,omitempty"`
}

func imageSessionUser(c *gin.Context) (int64, bool) {
	subject, ok := middleware.GetAuthSubjectFromContext(c)
	if !ok || subject.UserID <= 0 {
		c.JSON(http.StatusUnauthorized, gin.H{"message": "User not authenticated"})
		return 0, false
	}
	return subject.UserID, true
}

func validImageSessionID(value string) bool { _, err := uuid.Parse(value); return err == nil }

func (h *ImageSessionHandler) List(c *gin.Context) {
	userID, ok := imageSessionUser(c)
	if !ok {
		return
	}
	rows, err := h.db.QueryContext(c.Request.Context(), `SELECT id, title, sort_order, draft, created_at, updated_at FROM image_generation_sessions WHERE user_id = $1 ORDER BY sort_order DESC, created_at DESC`, userID)
	if err != nil {
		c.JSON(500, gin.H{"message": "Failed to list image sessions"})
		return
	}
	defer rows.Close()
	sessions := make([]gin.H, 0)
	for rows.Next() {
		var id, title string
		var order int64
		var draft []byte
		var created, updated time.Time
		if err := rows.Scan(&id, &title, &order, &draft, &created, &updated); err != nil {
			c.JSON(500, gin.H{"message": "Failed to read image sessions"})
			return
		}
		sessions = append(sessions, gin.H{"id": id, "title": title, "sortOrder": order, "createdAt": created.UnixMilli(), "updatedAt": updated.UnixMilli(), "draft": json.RawMessage(draft)})
	}
	if rows.Err() != nil {
		c.JSON(500, gin.H{"message": "Failed to list image sessions"})
		return
	}
	c.JSON(200, sessions)
}

func (h *ImageSessionHandler) Save(c *gin.Context) {
	userID, ok := imageSessionUser(c)
	if !ok {
		return
	}
	var input imageSessionInput
	if c.ShouldBindJSON(&input) != nil || !validImageSessionID(input.ID) || strings.TrimSpace(input.Title) == "" || len([]rune(input.Title)) > 80 {
		c.JSON(400, gin.H{"message": "Invalid image session"})
		return
	}
	if input.CreatedAt <= 0 {
		input.CreatedAt = time.Now().UnixMilli()
	}
	if input.UpdatedAt <= 0 {
		input.UpdatedAt = time.Now().UnixMilli()
	}
	result, err := h.db.ExecContext(c.Request.Context(), `INSERT INTO image_generation_sessions (id,user_id,title,sort_order,created_at,updated_at) VALUES ($1,$2,$3,$4,to_timestamp($5/1000.0),to_timestamp($6/1000.0)) ON CONFLICT (id) DO UPDATE SET title=EXCLUDED.title,sort_order=EXCLUDED.sort_order,updated_at=EXCLUDED.updated_at WHERE image_generation_sessions.user_id=$2`, input.ID, userID, strings.TrimSpace(input.Title), input.SortOrder, input.CreatedAt, input.UpdatedAt)
	if err != nil {
		c.JSON(500, gin.H{"message": "Failed to save image session"})
		return
	}
	count, _ := result.RowsAffected()
	if count == 0 {
		c.JSON(404, gin.H{"message": "Image session not found"})
		return
	}
	c.JSON(200, input)
}

func (h *ImageSessionHandler) Delete(c *gin.Context) {
	userID, ok := imageSessionUser(c)
	if !ok {
		return
	}
	id := c.Param("id")
	if !validImageSessionID(id) {
		c.JSON(404, gin.H{"message": "Image session not found"})
		return
	}
	result, err := h.db.ExecContext(c.Request.Context(), `DELETE FROM image_generation_sessions WHERE id=$1 AND user_id=$2`, id, userID)
	if err != nil {
		c.JSON(500, gin.H{"message": "Failed to delete image session"})
		return
	}
	count, _ := result.RowsAffected()
	if count == 0 {
		c.JSON(404, gin.H{"message": "Image session not found"})
		return
	}
	_ = os.RemoveAll(filepath.Join(h.sessionRoot, strconv.FormatInt(userID, 10), id))
	c.Status(204)
}

func (h *ImageSessionHandler) SaveDraft(c *gin.Context) {
	userID, ok := imageSessionUser(c)
	if !ok {
		return
	}
	var draft json.RawMessage
	if !validImageSessionID(c.Param("id")) || c.ShouldBindJSON(&draft) != nil || len(draft) > 1<<20 || !json.Valid(draft) {
		c.JSON(400, gin.H{"message": "Invalid image draft"})
		return
	}
	result, err := h.db.ExecContext(c.Request.Context(), `UPDATE image_generation_sessions SET draft=$1 WHERE id=$2 AND user_id=$3`, draft, c.Param("id"), userID)
	if err != nil {
		c.JSON(500, gin.H{"message": "Failed to save image draft"})
		return
	}
	count, _ := result.RowsAffected()
	if count == 0 {
		c.JSON(404, gin.H{"message": "Image session not found"})
		return
	}
	c.Status(204)
}

func (h *ImageSessionHandler) GetDraft(c *gin.Context) {
	userID, ok := imageSessionUser(c)
	if !ok {
		return
	}
	var draft []byte
	if !validImageSessionID(c.Param("id")) || h.db.QueryRowContext(c.Request.Context(), `SELECT draft FROM image_generation_sessions WHERE id=$1 AND user_id=$2`, c.Param("id"), userID).Scan(&draft) != nil {
		c.JSON(404, gin.H{"message": "Image session not found"})
		return
	}
	if len(draft) == 0 {
		c.Data(200, "application/json", []byte("null"))
		return
	}
	c.Data(200, "application/json", draft)
}

func (h *ImageSessionHandler) DeleteDraft(c *gin.Context) {
	userID, ok := imageSessionUser(c)
	if !ok {
		return
	}
	if !validImageSessionID(c.Param("id")) {
		c.Status(404)
		return
	}
	_, err := h.db.ExecContext(c.Request.Context(), `UPDATE image_generation_sessions SET draft=NULL WHERE id=$1 AND user_id=$2`, c.Param("id"), userID)
	if err != nil {
		c.JSON(500, gin.H{"message": "Failed to delete image draft"})
		return
	}
	c.Status(204)
}

func (h *ImageSessionHandler) ListRecords(c *gin.Context) {
	userID, ok := imageSessionUser(c)
	if !ok {
		return
	}
	rows, err := h.db.QueryContext(c.Request.Context(), `SELECT payload FROM image_generation_records WHERE user_id=$1 ORDER BY created_at DESC`, userID)
	if err != nil {
		c.JSON(500, gin.H{"message": "Failed to list image records"})
		return
	}
	defer rows.Close()
	records := make([]json.RawMessage, 0)
	for rows.Next() {
		var payload json.RawMessage
		if rows.Scan(&payload) != nil {
			c.JSON(500, gin.H{"message": "Failed to read image records"})
			return
		}
		records = append(records, payload)
	}
	if rows.Err() != nil {
		c.JSON(500, gin.H{"message": "Failed to list image records"})
		return
	}
	c.JSON(200, records)
}

func (h *ImageSessionHandler) GetRecord(c *gin.Context) {
	userID, ok := imageSessionUser(c)
	if !ok {
		return
	}
	var payload json.RawMessage
	if !validImageSessionID(c.Param("record")) || h.db.QueryRowContext(c.Request.Context(), `SELECT payload FROM image_generation_records WHERE id=$1 AND user_id=$2`, c.Param("record"), userID).Scan(&payload) != nil {
		c.JSON(404, gin.H{"message": "Image record not found"})
		return
	}
	c.Data(200, "application/json", payload)
}

func (h *ImageSessionHandler) SaveRecord(c *gin.Context) {
	userID, ok := imageSessionUser(c)
	if !ok {
		return
	}
	var input struct {
		ID        string `json:"id"`
		SessionID string `json:"sessionId"`
		CreatedAt int64  `json:"createdAt"`
	}
	var payload json.RawMessage
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 2<<20)
	if c.ShouldBindJSON(&payload) != nil || json.Unmarshal(payload, &input) != nil || !validImageSessionID(input.ID) || !validImageSessionID(input.SessionID) || input.CreatedAt <= 0 {
		c.JSON(400, gin.H{"message": "Invalid image record"})
		return
	}
	result, err := h.db.ExecContext(c.Request.Context(), `INSERT INTO image_generation_records (id,session_id,user_id,payload,created_at) SELECT $1,id,$3,$4,to_timestamp($5/1000.0) FROM image_generation_sessions WHERE id=$2 AND user_id=$3 ON CONFLICT (id) DO UPDATE SET payload=EXCLUDED.payload WHERE image_generation_records.user_id=$3 AND image_generation_records.session_id=EXCLUDED.session_id`, input.ID, input.SessionID, userID, payload, input.CreatedAt)
	if err != nil {
		c.JSON(500, gin.H{"message": "Failed to save image record"})
		return
	}
	count, _ := result.RowsAffected()
	if count == 0 {
		c.JSON(404, gin.H{"message": "Image session not found"})
		return
	}
	c.Status(204)
}

func (h *ImageSessionHandler) DeleteRecord(c *gin.Context) {
	userID, ok := imageSessionUser(c)
	if !ok {
		return
	}
	if !validImageSessionID(c.Param("record")) {
		c.JSON(404, gin.H{"message": "Image record not found"})
		return
	}
	_, err := h.db.ExecContext(c.Request.Context(), `DELETE FROM image_generation_records WHERE id=$1 AND user_id=$2`, c.Param("record"), userID)
	if err != nil {
		c.JSON(500, gin.H{"message": "Failed to delete image record"})
		return
	}
	c.Status(204)
}

func (h *ImageSessionHandler) UploadAsset(c *gin.Context) {
	userID, ok := imageSessionUser(c)
	if !ok {
		return
	}
	sessionID := c.Param("id")
	if !validImageSessionID(sessionID) {
		c.JSON(404, gin.H{"message": "Image session not found"})
		return
	}
	var exists bool
	if h.db.QueryRowContext(c.Request.Context(), `SELECT EXISTS(SELECT 1 FROM image_generation_sessions WHERE id=$1 AND user_id=$2)`, sessionID, userID).Scan(&exists) != nil || !exists {
		c.JSON(404, gin.H{"message": "Image session not found"})
		return
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maxSessionAssetBytes+1024)
	file, _, err := c.Request.FormFile("file")
	if err != nil {
		c.JSON(400, gin.H{"message": "Invalid image file"})
		return
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, maxSessionAssetBytes+1))
	if err != nil || len(data) == 0 || len(data) > maxSessionAssetBytes {
		c.JSON(400, gin.H{"message": "Image file exceeds 25 MB"})
		return
	}
	url, mime, err := h.storeAsset(c.Request.Context(), userID, sessionID, data)
	if err != nil {
		if errors.Is(err, os.ErrInvalid) {
			c.JSON(400, gin.H{"message": "Unsupported image format"})
			return
		}
		c.JSON(500, gin.H{"message": "Failed to store image"})
		return
	}
	c.JSON(200, gin.H{"url": url, "mimeType": mime, "fileSizeBytes": len(data)})
}

func (h *ImageSessionHandler) storeAsset(ctx context.Context, userID int64, sessionID string, data []byte) (string, string, error) {
	mime := http.DetectContentType(data)
	ext := ""
	switch mime {
	case "image/png":
		ext = ".png"
	case "image/jpeg":
		ext = ".jpg"
	case "image/webp":
		ext = ".webp"
	case "image/gif":
		ext = ".gif"
	default:
		return "", "", os.ErrInvalid
	}
	id := uuid.NewString()
	relative := filepath.Join(strconv.FormatInt(userID, 10), sessionID, id+ext)
	destination := filepath.Join(h.sessionRoot, relative)
	if err := os.MkdirAll(filepath.Dir(destination), 0755); err != nil {
		return "", "", err
	}
	tmp, err := os.CreateTemp(filepath.Dir(destination), ".upload-*")
	if err != nil {
		return "", "", err
	}
	defer os.Remove(tmp.Name())
	if _, err = tmp.Write(data); err == nil {
		err = tmp.Close()
	} else {
		_ = tmp.Close()
	}
	if err == nil {
		err = os.Rename(tmp.Name(), destination)
	}
	if err != nil {
		return "", "", err
	}
	_, err = h.db.ExecContext(ctx, `INSERT INTO image_generation_assets (id,session_id,user_id,path,mime_type,byte_size) VALUES ($1,$2,$3,$4,$5,$6)`, id, sessionID, userID, relative, mime, len(data))
	if err != nil {
		_ = os.Remove(destination)
		return "", "", err
	}
	return "/api/v1/image-sessions/assets/" + id, mime, nil
}

func (h *ImageSessionHandler) AttachTask(ctx context.Context, userID int64, sessionID, recordID, taskID string) error {
	result, err := h.db.ExecContext(ctx, `UPDATE image_generation_records SET payload=jsonb_set(payload,'{taskId}',to_jsonb($4::text)) WHERE id=$1 AND session_id=$2 AND user_id=$3`, recordID, sessionID, userID, taskID)
	if err != nil {
		return err
	}
	count, _ := result.RowsAffected()
	if count == 0 {
		return sql.ErrNoRows
	}
	return nil
}

func (h *ImageSessionHandler) CompleteTask(ctx context.Context, taskID string, result json.RawMessage) error {
	var recordID, sessionID string
	var userID int64
	var payload []byte
	err := h.db.QueryRowContext(ctx, `SELECT id,session_id,user_id,payload FROM image_generation_records WHERE payload->>'taskId'=$1`, taskID).Scan(&recordID, &sessionID, &userID, &payload)
	if err == sql.ErrNoRows {
		return nil
	}
	if err != nil {
		return err
	}
	var response struct {
		Data []struct {
			URL           string `json:"url"`
			Base64        string `json:"b64_json"`
			RevisedPrompt string `json:"revised_prompt"`
		} `json:"data"`
	}
	if err := json.Unmarshal(result, &response); err != nil {
		return err
	}
	if len(response.Data) == 0 {
		return os.ErrInvalid
	}
	images := make([]gin.H, 0, len(response.Data))
	for _, item := range response.Data {
		var data []byte
		if item.Base64 != "" {
			data, err = base64.StdEncoding.DecodeString(item.Base64)
		} else {
			const prefix = "/v1/images/storage/"
			parsed, parseErr := url.Parse(item.URL)
			if parseErr != nil || parsed.IsAbs() || !strings.HasPrefix(parsed.Path, prefix) {
				return os.ErrInvalid
			}
			key, pathErr := url.PathUnescape(strings.TrimPrefix(parsed.Path, prefix))
			clean := filepath.Clean(filepath.FromSlash(key))
			if pathErr != nil || clean == ".." || filepath.IsAbs(clean) || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
				return os.ErrInvalid
			}
			data, err = os.ReadFile(filepath.Join(h.imageStorageRoot, clean))
		}
		if err != nil || len(data) == 0 || len(data) > maxSessionAssetBytes {
			return os.ErrInvalid
		}
		assetURL, mime, saveErr := h.storeAsset(ctx, userID, sessionID, data)
		if saveErr != nil {
			return saveErr
		}
		images = append(images, gin.H{"url": assetURL, "mimeType": mime, "fileSizeBytes": len(data), "revisedPrompt": item.RevisedPrompt})
	}
	var record map[string]any
	if err := json.Unmarshal(payload, &record); err != nil {
		return err
	}
	now := time.Now().UnixMilli()
	record["images"] = images
	record["status"] = "completed"
	record["completedAt"] = now
	if created, ok := record["createdAt"].(float64); ok {
		record["durationMs"] = now - int64(created)
	}
	updated, err := json.Marshal(record)
	if err != nil {
		return err
	}
	_, err = h.db.ExecContext(ctx, `UPDATE image_generation_records SET payload=$1 WHERE id=$2 AND user_id=$3`, updated, recordID, userID)
	return err
}

func (h *ImageSessionHandler) FailTask(ctx context.Context, taskID string, taskErr json.RawMessage) error {
	var detail struct {
		Message string `json:"message"`
	}
	_ = json.Unmarshal(taskErr, &detail)
	_, err := h.db.ExecContext(ctx, `UPDATE image_generation_records SET payload=jsonb_set(jsonb_set(jsonb_set(payload,'{status}',to_jsonb('failed'::text)),'{error}',to_jsonb($2::text)),'{completedAt}',to_jsonb($3::bigint)) WHERE payload->>'taskId'=$1`, taskID, detail.Message, time.Now().UnixMilli())
	return err
}

func (h *ImageSessionHandler) ReadAsset(c *gin.Context) {
	userID, ok := imageSessionUser(c)
	if !ok {
		return
	}
	if !validImageSessionID(c.Param("asset")) {
		c.Status(404)
		return
	}
	var relative, mime string
	if h.db.QueryRowContext(c.Request.Context(), `SELECT path,mime_type FROM image_generation_assets WHERE id=$1 AND user_id=$2`, c.Param("asset"), userID).Scan(&relative, &mime) != nil {
		c.Status(404)
		return
	}
	// The path is written only by UploadAsset; still reject malformed database values.
	clean := filepath.Clean(relative)
	if filepath.IsAbs(clean) || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		c.Status(404)
		return
	}
	c.Header("X-Content-Type-Options", "nosniff")
	c.Header("Cache-Control", "private, max-age=3600")
	c.Header("Content-Type", mime)
	c.File(filepath.Join(h.sessionRoot, clean))
}
