package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"time"
)

type Config struct {
	ServerURL  string `json:"server_url"`
	DeviceID   string `json:"device_id"`
	Channel    string `json:"channel"`
	InstallDir string `json:"install_dir"`
	CheckEvery int    `json:"check_every_seconds"`
}

type Release struct {
	Version string `json:"version"`
	Channel string `json:"channel"`
	URL     string `json:"url"`
	Sha256  string `json:"sha256"`
	Notes   string `json:"notes"`
}

type CheckResp struct {
	UpdateAvailable bool     `json:"update_available"`
	Latest          *Release `json:"latest"`
	Message         string   `json:"message"`
}

var (
	currentCmd   *exec.Cmd
	currentVerFP string
)

func main() {
	if len(os.Args) < 2 {
		log.Fatalf("Usage: %s /path/to/config.json", os.Args[0])
	}
	cfg, err := loadConfig(os.Args[1])
	if err != nil {
		log.Fatal(err)
	}
	if cfg.CheckEvery <= 0 {
		cfg.CheckEvery = 10
	}
	if err := os.MkdirAll(cfg.InstallDir, 0o755); err != nil {
		log.Fatal(err)
	}
	currentVerFP = filepath.Join(cfg.InstallDir, "current_version")

	// 启动已有版本（若存在）
	currLink := filepath.Join(cfg.InstallDir, "algo_current")
	if _, err := os.Stat(currLink); err == nil {
		if err := startAlgorithm(currLink); err != nil {
			log.Printf("start current algo failed: %v", err)
		}
	} else {
		log.Printf("no current algo yet, waiting for first update...")
	}

	ticker := time.NewTicker(time.Duration(cfg.CheckEvery) * time.Second)
	defer ticker.Stop()

	for {
		if err := runOnce(cfg, readCurrentVersion()); err != nil {
			log.Printf("check/update error: %v", err)
		}
		<-ticker.C
	}
}

func runOnce(cfg *Config, current string) error {
	u := cfg.ServerURL + "/check?channel=" + cfg.Channel + "&current=" + current + "&device_id=" + cfg.DeviceID
	resp, err := http.Get(u)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		b, _ := io.ReadAll(resp.Body)
		return errors.New("check failed: " + string(b))
	}
	var ck CheckResp
	if err := json.NewDecoder(resp.Body).Decode(&ck); err != nil {
		return err
	}
	if !ck.UpdateAvailable || ck.Latest == nil {
		log.Printf("no update. current=%s", current)
		return nil
	}
	log.Printf("new version: %s (%s)", ck.Latest.Version, ck.Latest.Channel)

	// 下载到临时文件
	downloadURL := cfg.ServerURL + ck.Latest.URL
	tmpFile := filepath.Join(cfg.InstallDir, "download_"+ck.Latest.Version)
	if err := downloadToFile(downloadURL, tmpFile); err != nil {
		return err
	}

	// 校验 sha256
	ok, err := verifySha256(tmpFile, ck.Latest.Sha256)
	if err != nil {
		return err
	}
	if !ok {
		_ = os.Remove(tmpFile)
		return errors.New("sha256 mismatch")
	}

	// 安装为 algo_<version>
	dst := filepath.Join(cfg.InstallDir, "algo_"+ck.Latest.Version)
	if err := os.Rename(tmpFile, dst); err != nil {
		return err
	}
	if err := os.Chmod(dst, 0o755); err != nil { // 确保可执行
		return err
	}

	// 原子切换符号链接
	currLink := filepath.Join(cfg.InstallDir, "algo_current")
	_ = os.Remove(currLink)
	if err := os.Symlink(dst, currLink); err != nil {
		return err
	}

	// 平滑重启
	if err := restartAlgorithm(currLink); err != nil {
		return err
	}

	// 记录当前版本
	if err := os.WriteFile(currentVerFP, []byte(ck.Latest.Version), 0o644); err != nil {
		return err
	}
	log.Printf("updated to %s", ck.Latest.Version)
	return nil
}

func loadConfig(fp string) (*Config, error) {
	b, err := os.ReadFile(fp)
	if err != nil {
		return nil, err
	}
	var c Config
	if err := json.Unmarshal(b, &c); err != nil {
		return nil, err
	}
	return &c, nil
}

func readCurrentVersion() string {
	b, err := os.ReadFile(currentVerFP)
	if err != nil {
		return ""
	}
	return string(b)
}

func downloadToFile(url, dst string) error {
	resp, err := http.Get(url)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		b, _ := io.ReadAll(resp.Body)
		return errors.New("download failed: " + string(b))
	}
	f, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = io.Copy(f, resp.Body)
	return err
}

func verifySha256(fp, want string) (bool, error) {
	f, err := os.Open(fp)
	if err != nil {
		return false, err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return false, err
	}
	got := hex.EncodeToString(h.Sum(nil))
	return got == want, nil
}

func startAlgorithm(bin string) error {
	cmd := exec.Command(bin)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		return err
	}
	currentCmd = cmd
	log.Printf("algorithm started (pid=%d)", cmd.Process.Pid)
	go func() {
		err := cmd.Wait()
		log.Printf("algorithm exited: %v", err)
	}()
	return nil
}

func stopAlgorithm() error {
	if currentCmd == nil || currentCmd.Process == nil {
		return nil
	}
	if err := currentCmd.Process.Signal(os.Interrupt); err != nil {
		_ = currentCmd.Process.Kill()
	}
	currentCmd = nil
	return nil
}

func restartAlgorithm(bin string) error {
	if err := stopAlgorithm(); err != nil {
		return err
	}
	time.Sleep(300 * time.Millisecond)
	return startAlgorithm(bin)
}
