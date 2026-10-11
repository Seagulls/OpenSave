package cloud

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/opensave/opensave/internal/store"
)

func webDAVIntegrityService(t *testing.T, handler http.Handler) (*Service, *httptest.Server) {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	svc, db := newTestService(t)
	setCloudConfig(t, db, func(c *store.CloudConfig) {
		c.Enabled = true
		c.Provider = "webdav"
		c.URL = server.URL + "/dav"
	})
	return svc, server
}

func TestWebDAVUploadRejectsTruncatedOrCorruptReadback(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func([]byte) []byte
	}{
		{
			name: "truncated",
			mutate: func(body []byte) []byte {
				return append([]byte(nil), body[:len(body)/3]...)
			},
		},
		{
			name: "same length corruption",
			mutate: func(body []byte) []byte {
				got := append([]byte(nil), body...)
				got[len(got)/2] ^= 0xff
				return got
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var stored []byte
			svc, _ := webDAVIntegrityService(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.Method {
				case http.MethodPut:
					body, _ := io.ReadAll(r.Body)
					stored = tc.mutate(body)
					w.WriteHeader(http.StatusCreated)
				case http.MethodGet:
					w.Header().Set("Content-Length", fmt.Sprint(len(stored)))
					_, _ = w.Write(stored)
				default:
					t.Fatalf("unexpected method %s", r.Method)
				}
			}))

			err := svc.Upload(writeTempZip(t, strings.Repeat("synthetic zip bytes", 9)), "game__main__snap_1.zip")
			if err == nil || !strings.Contains(err.Error(), "verification failed") {
				t.Fatalf("Upload error = %v, want verification failure", err)
			}
		})
	}
}

func TestWebDAVUploadVerificationReadbackFailures(t *testing.T) {
	for _, status := range []int{
		http.StatusForbidden,
		http.StatusNotFound,
		http.StatusInternalServerError,
		http.StatusServiceUnavailable,
	} {
		t.Run(fmt.Sprintf("HTTP_%d", status), func(t *testing.T) {
			svc, _ := webDAVIntegrityService(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method == http.MethodPut {
					_, _ = io.Copy(io.Discard, r.Body)
					w.WriteHeader(http.StatusCreated)
					return
				}
				w.WriteHeader(status)
			}))
			err := svc.Upload(writeTempZip(t, "synthetic zip"), "game__main__snap_1.zip")
			if err == nil || !strings.Contains(err.Error(), fmt.Sprintf("HTTP %d", status)) {
				t.Fatalf("Upload error = %v, want HTTP %d verification failure", err, status)
			}
		})
	}
}

func TestWebDAVUploadVerificationRejectsInterruptedReadback(t *testing.T) {
	var stored []byte
	svc, _ := webDAVIntegrityService(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodPut:
			stored, _ = io.ReadAll(r.Body)
			w.WriteHeader(http.StatusCreated)
		case http.MethodGet:
			w.Header().Set("Content-Length", fmt.Sprint(len(stored)+100))
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write(stored[:len(stored)/2])
		}
	}))
	err := svc.Upload(writeTempZip(t, strings.Repeat("synthetic zip", 20)), "game__main__snap_1.zip")
	if err == nil || !strings.Contains(err.Error(), "verification failed") {
		t.Fatalf("Upload error = %v, want interrupted verification failure", err)
	}
}

func TestWebDAVUploadVerificationRejectsReadbackTimeout(t *testing.T) {
	var stored []byte
	svc, _ := webDAVIntegrityService(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodPut:
			stored, _ = io.ReadAll(r.Body)
			w.WriteHeader(http.StatusCreated)
		case http.MethodGet:
			time.Sleep(100 * time.Millisecond)
			_, _ = w.Write(stored)
		}
	}))
	oldTimeout := perRequestTransferTimeout
	perRequestTransferTimeout = 10 * time.Millisecond
	t.Cleanup(func() { perRequestTransferTimeout = oldTimeout })
	err := svc.Upload(writeTempZip(t, "synthetic zip"), "game__main__snap_1.zip")
	if err == nil || !strings.Contains(err.Error(), "verification failed") {
		t.Fatalf("Upload error = %v, want timeout verification failure", err)
	}
}

func TestWebDAVUploadVerificationDoesNotRequireHEAD(t *testing.T) {
	for _, tc := range []struct {
		name       string
		headStatus int
	}{
		{name: "HEAD unsupported", headStatus: http.StatusMethodNotAllowed},
		{name: "HEAD unavailable", headStatus: http.StatusServiceUnavailable},
		{name: "HEAD missing length", headStatus: http.StatusOK},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var stored []byte
			svc, _ := webDAVIntegrityService(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.Method {
				case http.MethodPut:
					stored, _ = io.ReadAll(r.Body)
					w.WriteHeader(http.StatusCreated)
				case http.MethodHead:
					w.WriteHeader(tc.headStatus)
				case http.MethodGet:
					_, _ = w.Write(stored)
				}
			}))
			if err := svc.Upload(writeTempZip(t, "complete synthetic zip"), "game__main__snap_1.zip"); err != nil {
				t.Fatalf("Upload: %v", err)
			}
		})
	}
}

func TestWebDAVUploadVerificationSendsCredentialsAndCustomHeaders(t *testing.T) {
	var stored []byte
	var getAuth, getCustom string
	svc, _ := webDAVIntegrityService(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodPut:
			stored, _ = io.ReadAll(r.Body)
			w.WriteHeader(http.StatusCreated)
		case http.MethodGet:
			getAuth = r.Header.Get("Authorization")
			getCustom = r.Header.Get("X-Test-Token")
			_, _ = w.Write(stored)
		}
	}))
	setCloudConfig(t, svc.Store, func(c *store.CloudConfig) {
		c.Username = "alice"
		c.Password = "secret"
		c.HeadersJSON = `{"X-Test-Token":"present"}`
	})
	if err := svc.Upload(writeTempZip(t, "complete synthetic zip"), "game__main__snap_1.zip"); err != nil {
		t.Fatalf("Upload: %v", err)
	}
	if getAuth == "" || getCustom != "present" {
		t.Fatalf("verification headers: Authorization=%q X-Test-Token=%q", getAuth, getCustom)
	}
}

func TestLocalFolderListPropagatesDirectoryErrors(t *testing.T) {
	t.Run("absent directory", func(t *testing.T) {
		missing := filepath.Join(t.TempDir(), "missing-share")
		files, err := (localFolder{cfg: store.CloudConfig{URL: missing}}).list()
		if err == nil {
			t.Fatalf("list = %+v, nil error; want missing-directory error", files)
		}
	})

	t.Run("configured path is not a directory", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "not-a-directory")
		if err := os.WriteFile(path, []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
		files, err := (localFolder{cfg: store.CloudConfig{URL: path}}).list()
		if err == nil {
			t.Fatalf("list = %+v, nil error; want directory-read error", files)
		}
	})
}

func TestLocalFolderListStillSupportsOrdinaryFolders(t *testing.T) {
	dir := t.TempDir()
	want := []byte("synthetic zip")
	if err := os.WriteFile(filepath.Join(dir, "game__main__snap_1.zip"), want, 0o600); err != nil {
		t.Fatal(err)
	}
	files, err := (localFolder{cfg: store.CloudConfig{URL: dir}}).list()
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(files) != 1 || files[0].Name != "game__main__snap_1.zip" || files[0].SizeBytes != int64(len(want)) {
		t.Fatalf("list = %+v", files)
	}
}

func TestWebDAVUploadVerificationPositiveControl(t *testing.T) {
	payload := []byte(strings.Repeat("complete synthetic zip", 8))
	var stored []byte
	svc, _ := webDAVIntegrityService(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodPut:
			stored, _ = io.ReadAll(r.Body)
			w.WriteHeader(http.StatusCreated)
		case http.MethodGet:
			_, _ = w.Write(stored)
		}
	}))
	src := filepath.Join(t.TempDir(), "snap.zip")
	if err := os.WriteFile(src, payload, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := svc.Upload(src, "game__main__snap_1.zip"); err != nil {
		t.Fatalf("Upload: %v", err)
	}
	if !bytes.Equal(stored, payload) {
		t.Fatal("positive control server did not receive the source bytes")
	}
}
