package stick

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"time"
)

// Artifact is the stream's metal ISO for x86_64.
type Artifact struct {
	Release  string
	Location string
	SHA256   string
}

// Stream reads builds.coreos.fedoraproject.org/streams/<stream>.json.
func Stream(stream string) (Artifact, error) {
	resp, err := http.Get("https://builds.coreos.fedoraproject.org/streams/" + stream + ".json")
	if err != nil {
		return Artifact{}, err
	}
	defer resp.Body.Close()
	var doc struct {
		Architectures map[string]struct {
			Artifacts map[string]struct {
				Release string `json:"release"`
				Formats map[string]struct {
					Disk struct {
						Location string `json:"location"`
						SHA256   string `json:"sha256"`
					} `json:"disk"`
				} `json:"formats"`
			} `json:"artifacts"`
		} `json:"architectures"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&doc); err != nil {
		return Artifact{}, err
	}
	metal := doc.Architectures["x86_64"].Artifacts["metal"]
	iso := metal.Formats["iso"].Disk
	if iso.Location == "" {
		return Artifact{}, fmt.Errorf("stream %s has no x86_64 metal iso", stream)
	}
	return Artifact{Release: metal.Release, Location: iso.Location, SHA256: iso.SHA256}, nil
}

// Download fetches the ISO into dir (cached by sha256) and verifies it.
// progress gets the bytes so far and the total.
func Download(a Artifact, dir string, progress func(done, total int64)) (string, error) {
	os.MkdirAll(dir, 0o755)
	dst := filepath.Join(dir, "fedora-coreos-"+a.Release+"-live.x86_64.iso")
	if sum, err := fileSHA256(dst); err == nil && sum == a.SHA256 {
		return dst, nil
	}
	resp, err := http.Get(a.Location)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("download: %s", resp.Status)
	}
	tmp := dst + ".part"
	f, err := os.Create(tmp)
	if err != nil {
		return "", err
	}
	h := sha256.New()
	var done int64
	buf := make([]byte, 1<<20)
	last := time.Now()
	for {
		n, rerr := resp.Body.Read(buf)
		if n > 0 {
			f.Write(buf[:n])
			h.Write(buf[:n])
			done += int64(n)
			if progress != nil && time.Since(last) > 500*time.Millisecond {
				progress(done, resp.ContentLength)
				last = time.Now()
			}
		}
		if rerr == io.EOF {
			break
		}
		if rerr != nil {
			f.Close()
			return "", rerr
		}
	}
	f.Close()
	if progress != nil {
		progress(done, resp.ContentLength)
	}
	if got := hex.EncodeToString(h.Sum(nil)); got != a.SHA256 {
		os.Remove(tmp)
		return "", fmt.Errorf("checksum mismatch: %s != %s", got, a.SHA256)
	}
	return dst, os.Rename(tmp, dst)
}

func fileSHA256(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
