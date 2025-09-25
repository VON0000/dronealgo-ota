package controller

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"github.com/gin-gonic/gin"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

type FileController struct {
	BaseController
}

type Release struct {
	Version   string    `json:"version"`
	Channel   string    `json:"channel"` // e.g. "stable", "beta"
	URL       string    `json:"url"`     // relative: /download/<version>
	Sha256    string    `json:"sha256"`
	Notes     string    `json:"notes"`
	CreatedAt time.Time `json:"created_at"`
	FilePath  string    `json:"-"`
}

type Store struct {
	mu                sync.RWMutex
	ReleasesByVersion map[string]*Release `json:"releases_by_version"`
	LatestByChannel   map[string]string   `json:"latest_by_channel"` // channel -> version
}

var (
	dataDir   = "../../data"
	artDir    = "../../artifacts"
	storeFile = filepath.Join(dataDir, "releases.json")
	store     = &Store{
		ReleasesByVersion: map[string]*Release{},
		LatestByChannel:   map[string]string{},
	}
)

func saveStore() error {
	if err := os.MkdirAll(filepath.Dir(storeFile), 0755); err != nil {
		return err
	}
	tmp := storeFile + ".tmp"
	f, err := os.Create(tmp)
	if err != nil {
		return err
	}
	if err := json.NewEncoder(f).Encode(store); err != nil {
		_ = f.Close()
		return err
	}
	_ = f.Close()
	return os.Rename(tmp, storeFile)
}

func (c *FileController) Publish(g *gin.Context) {
	version := strings.TrimSpace(g.PostForm("version"))
	if version == "" {
		c.ResponseFailure(g, ErrParam, "version is required")
	}

	channel := strings.TrimSpace(g.PostForm("channel"))
	if channel == "" {
		channel = "stable"
	}

	notes := strings.TrimSpace(g.PostForm("notes"))

	fileHeader, err := g.FormFile("file")
	if err != nil {
		c.ResponseFailure(g, ErrParam, "missing file: "+err.Error())
	}

	// 可选：限制单接口上传大小（例如 50MB）
	g.Request.Body = http.MaxBytesReader(g.Writer, g.Request.Body, 100<<20)

	vDir := filepath.Join(artDir, version)
	if err := os.MkdirAll(vDir, 0755); err != nil {
		c.ResponseFailure(g, ErrInternal, err.Error())
	}
	dstPath := filepath.Join(vDir, "algorithm")
	dst, err := os.Create(dstPath)
	if err != nil {
		c.ResponseFailure(g, ErrInternal, "create dst: "+err.Error())
		return
	}
	defer dst.Close()

	f, err := os.Open(dstPath)
	if err != nil {
		c.ResponseFailure(g, ErrInternal, "open dst: "+err.Error())
		return
	}
	defer f.Close()

	src, err := fileHeader.Open()
	if err != nil {
		c.ResponseFailure(g, ErrInternal, "open upload: "+err.Error())
		return
	}
	defer src.Close()

	h := sha256.New()
	if _, err := io.Copy(io.MultiWriter(dst, h), f); err != nil {
		c.ResponseFailure(g, ErrInternal, "hash: "+err.Error())
		return
	}
	sum := hex.EncodeToString(h.Sum(nil))

	url := "/download/" + version
	rel := &Release{
		Version:   version,
		Channel:   channel,
		URL:       url,
		Sha256:    sum,
		Notes:     notes,
		CreatedAt: time.Now(),
		FilePath:  dstPath,
	}

	store.mu.Lock()
	defer store.mu.Unlock()
	store.ReleasesByVersion[version] = rel
	store.LatestByChannel[channel] = version

	if err := saveStore(); err != nil {
		c.ResponseFailure(g, ErrInternal, "save metadata: "+err.Error())
		return
	}

	g.JSON(http.StatusOK, rel)
}
