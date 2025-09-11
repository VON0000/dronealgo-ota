package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

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

func main() {
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		log.Fatal(err)
	}
	if err := os.MkdirAll(artDir, 0o755); err != nil {
		log.Fatal(err)
	}
	if err := loadStore(); err != nil {
		log.Printf("loadStore warn: %v", err)
	}

	http.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, "ok")
	})

	http.HandleFunc("/publish", handlePublish)
	http.HandleFunc("/check", handleCheck)
	http.HandleFunc("/download/", handleDownload)

	addr := ":8080"
	log.Printf("Server listening at %s", addr)
	log.Fatal(http.ListenAndServe(addr, nil))
}

func handlePublish(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST only", http.StatusMethodNotAllowed)
		return
	}
	if err := r.ParseMultipartForm(100 << 20); err != nil { // 100MB
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	version := strings.TrimSpace(r.FormValue("version"))
	if version == "" {
		http.Error(w, "missing version", http.StatusBadRequest)
		return
	}
	channel := r.FormValue("channel")
	if channel == "" {
		channel = "stable"
	}
	notes := r.FormValue("notes")

	file, header, err := r.FormFile("file")
	if err != nil {
		http.Error(w, "missing file: "+err.Error(), http.StatusBadRequest)
		return
	}
	defer file.Close()

	// Save artifact
	vDir := filepath.Join(artDir, version)
	if err := os.MkdirAll(vDir, 0o755); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	dstPath := filepath.Join(vDir, "algorithm")
	dst, err := os.Create(dstPath)
	if err != nil {
		http.Error(w, "create dst: "+err.Error(), http.StatusInternalServerError)
		return
	}
	defer dst.Close()

	h := sha256.New()
	n, err := io.Copy(io.MultiWriter(dst, h), file)
	if err != nil {
		http.Error(w, "save file: "+err.Error(), http.StatusInternalServerError)
		return
	}
	sum := hex.EncodeToString(h.Sum(nil))

	log.Printf("Uploaded %s (%d bytes) as %s", header.Filename, n, dstPath)
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
		http.Error(w, "save metadata: "+err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(rel)
}

func handleCheck(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	channel := q.Get("channel")
	if channel == "" {
		channel = "stable"
	}
	current := q.Get("current")
	_ = q.Get("device_id") // reserved for future use

	store.mu.RLock()
	defer store.mu.RUnlock()

	latestVersion, ok := store.LatestByChannel[channel]
	if !ok {
		http.Error(w, "no release in channel", http.StatusNotFound)
		return
	}
	latest := store.ReleasesByVersion[latestVersion]
	resp := map[string]any{
		"update_available": false,
		"latest":           latest,
		"message":          "up to date",
	}

	if current == "" || isNewer(latest.Version, current) {
		resp["update_available"] = true
		resp["message"] = "new version available"
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

func handleDownload(w http.ResponseWriter, r *http.Request) {
	// /download/<version>
	parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/download/"), "/")
	if len(parts) == 0 || parts[0] == "" {
		http.Error(w, "version required", http.StatusBadRequest)
		return
	}
	version := parts[0]

	store.mu.RLock()
	rel, ok := store.ReleasesByVersion[version]
	store.mu.RUnlock()
	if !ok {
		http.Error(w, "unknown version", http.StatusNotFound)
		return
	}

	// Serve file
	w.Header().Set("Content-Disposition", "attachment; filename="+strconv.Quote(path.Base(rel.FilePath)))
	http.ServeFile(w, r, rel.FilePath)
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

func loadStore() error {
	f, err := os.Open(storeFile)
	if err != nil {
		return err
	}
	defer f.Close()
	dec := json.NewDecoder(f)
	return dec.Decode(store)
}

func saveStore() error {
	tmp := storeFile + ".tmp"
	f, err := os.Create(tmp)
	if err != nil {
		return err
	}
	if err := json.NewEncoder(f).Encode(store); err != nil {
		f.Close()
		return err
	}
	f.Close()
	return os.Rename(tmp, storeFile)
}
