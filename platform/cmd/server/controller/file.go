package controller

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"github.com/gin-gonic/gin"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
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

func InitStore() error {
	if err := loadStore(); err != nil {
		return err
	}
	return nil
}

func loadStore() error {
	f, err := os.Open(storeFile)
	if err != nil {
		return err
	}
	defer f.Close()

	tmp := &Store{}
	tmp.ReleasesByVersion = map[string]*Release{}
	tmp.LatestByChannel = map[string]string{}

	if err := json.NewDecoder(f).Decode(tmp); err != nil {
		return err
	}

	store.mu.Lock()
	defer store.mu.Unlock()

	store.ReleasesByVersion = tmp.ReleasesByVersion
	store.LatestByChannel = tmp.LatestByChannel
	return nil
}

func saveStore() error {
	if err := os.MkdirAll(filepath.Dir(storeFile), 0755); err != nil {
		return err
	}
	tmp := storeFile + ".tmp"
	f, err := os.Create(tmp)
	if err != nil {
		return err
	}
	enc := json.NewEncoder(f)
	enc.SetIndent("", "  ")
	if err := enc.Encode(store); err != nil {
		f.Close()
		return err
	}
	_ = f.Close()
	return os.Rename(tmp, storeFile)
}

// Publish godoc
// @Summary      Publish an algorithm artifact
// @Description  Upload the algorithm binary and create a release record.
// @Tags         release
// @Accept       mpfd
// @Produce      json
// @Param        version  formData  string  true   "Version (e.g. 1.1.0)"
// @Param        channel  formData  string  false  "Channel (stable|beta), default: stable"
// @Param        notes    formData  string  false  "Release notes"
// @Param        file     formData  file    true   "Algorithm binary"
// @Success      200  {object}  controller.Release
// @Failure      400  {object}  map[string]any
// @Failure      500  {object}  map[string]any
// @Router       /api/v1/publish [post]
func (c *FileController) Publish(g *gin.Context) {
	// 可选：限制单接口上传大小（例如 50MB）
	g.Request.Body = http.MaxBytesReader(g.Writer, g.Request.Body, 100<<20)

	version := strings.TrimSpace(g.PostForm("version"))
	if version == "" {
		c.ResponseFailure(g, ErrParam, "version is required")
		return
	}

	channel := strings.TrimSpace(g.PostForm("channel"))
	if channel == "" {
		channel = "stable"
	}

	notes := strings.TrimSpace(g.PostForm("notes"))

	fileHeader, err := g.FormFile("file")
	if err != nil {
		c.ResponseFailure(g, ErrParam, "missing file: "+err.Error())
		return
	}

	vDir := filepath.Join(artDir, version)
	if err := os.MkdirAll(vDir, 0755); err != nil {
		c.ResponseFailure(g, ErrInternal, err.Error())
		return
	}
	dstPath := filepath.Join(vDir, "algorithm")
	dst, err := os.Create(dstPath)
	if err != nil {
		c.ResponseFailure(g, ErrInternal, "create dst: "+err.Error())
		return
	}
	defer dst.Close()

	src, err := fileHeader.Open()
	if err != nil {
		c.ResponseFailure(g, ErrInternal, "open upload: "+err.Error())
		return
	}
	defer src.Close()

	h := sha256.New()
	if _, err := io.Copy(io.MultiWriter(dst, h), src); err != nil {
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

func isNewer(a, b string) bool {
	// Compare SemVer-like: "MAJ.MIN.PATCH[-extra]" (very simple)
	parse := func(s string) (int, int, int) {
		s = strings.SplitN(s, "-", 2)[0]
		parts := strings.Split(s, ".")
		get := func(i int) int {
			if i >= len(parts) {
				return 0
			}
			n, _ := strconv.Atoi(parts[i])
			return n
		}
		return get(0), get(1), get(2)
	}

	amaj, amin, apat := parse(a)
	bmaj, bmin, bpat := parse(b)

	if amaj != bmaj {
		return amaj > bmaj
	}
	if amin != bmin {
		return amin > bmin
	}
	return apat > bpat
}

// Check godoc
// @Summary      Check for updates
// @Description  Check whether a newer version is available under the channel.
// @Tags         release
// @Produce      json
// @Param        channel  query  string  false  "Channel (stable|beta), default: stable"
// @Param        current  query  string  false  "Current version on device"
// @Success      200  {object}  map[string]any  "update_available, latest, message"
// @Failure      400  {object}  map[string]any
// @Failure      500  {object}  map[string]any
// @Router       /api/v1/check [get]
func (c *FileController) Check(g *gin.Context) {
	if err := loadStore(); err != nil {
		log.Printf("loadStore warn: %v", err)
	}

	channel := g.DefaultQuery("channel", "stable")
	current := g.Query("current")

	store.mu.RLock()
	defer store.mu.RUnlock()

	latestVersion, ok := store.LatestByChannel[channel]
	if !ok {
		c.ResponseFailure(g, ErrInternal, "no release in channel")
		return
	}

	latest := store.ReleasesByVersion[latestVersion]

	resp := gin.H{
		"update_available": false,
		"latest":           latest,
		"message":          "up to date",
	}

	if current == "" || isNewer(latest.Version, current) {
		resp["update_available"] = true
		resp["message"] = "new version available"
	}

	g.JSON(http.StatusOK, resp)
}
