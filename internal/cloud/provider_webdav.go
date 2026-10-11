package cloud

import (
	"crypto/sha256"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path"
	"strconv"
	"strings"
	"time"

	"github.com/opensave/opensave/internal/store"
)

// webDAV keeps snapshots in a folder on a WebDAV server (Nextcloud, a NAS).
type webDAV struct {
	s   *Service
	cfg store.CloudConfig
}

func (p webDAV) upload(f *os.File, size int64, fileName string) error {
	if p.cfg.URL == "" {
		return fmt.Errorf("no destination URL configured")
	}
	localHash, err := hashFile(f)
	if err != nil {
		return fmt.Errorf("hash WebDAV upload source: %w", err)
	}
	target := joinURL(p.cfg.URL, url.PathEscape(fileName))
	req, err := http.NewRequest(http.MethodPut, target, f)
	if err != nil {
		return err
	}
	req.ContentLength = size
	req.Header.Set("Content-Type", "application/zip")
	applyCustomHeaders(req, p.cfg.HeadersJSON)
	applyBasicAuth(req, p.cfg.Username, p.cfg.Password)
	resp, err := p.s.doTransfer(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if err := transferOK(resp); err != nil {
		return err
	}

	// A successful PUT only says that the server accepted the request. Read the
	// object back and compare its digest so a truncated or same-length-corrupt
	// object is never reported as a verified upload. GET is deliberate here:
	// Content-Length from HEAD cannot prove byte integrity, and some otherwise
	// usable WebDAV implementations do not implement HEAD reliably.
	remoteHash, remoteSize, err := p.hashRemote(target, size)
	if err != nil {
		return fmt.Errorf("WebDAV upload verification failed for %s: %w", fileName, err)
	}
	if remoteSize != size || remoteHash != localHash {
		return fmt.Errorf("WebDAV upload verification failed for %s: remote content differs (stored %d of %d bytes)",
			fileName, remoteSize, size)
	}
	return nil
}

func hashFile(f *os.File) ([sha256.Size]byte, error) {
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return [sha256.Size]byte{}, err
	}
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return [sha256.Size]byte{}, err
	}
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return [sha256.Size]byte{}, err
	}
	return [sha256.Size]byte(h.Sum(nil)), nil
}

func (p webDAV) hashRemote(target string, expectedSize int64) ([sha256.Size]byte, int64, error) {
	req, err := http.NewRequest(http.MethodGet, target, nil)
	if err != nil {
		return [sha256.Size]byte{}, 0, err
	}
	applyCustomHeaders(req, p.cfg.HeadersJSON)
	applyBasicAuth(req, p.cfg.Username, p.cfg.Password)
	resp, err := p.s.doTransfer(req)
	if err != nil {
		return [sha256.Size]byte{}, 0, err
	}
	defer resp.Body.Close()
	if err := transferOK(resp); err != nil {
		return [sha256.Size]byte{}, 0, err
	}
	if resp.ContentLength >= 0 && resp.ContentLength != expectedSize {
		return [sha256.Size]byte{}, resp.ContentLength, nil
	}
	h := sha256.New()
	n, err := io.Copy(h, io.LimitReader(resp.Body, expectedSize+1))
	if err != nil {
		return [sha256.Size]byte{}, n, err
	}
	return [sha256.Size]byte(h.Sum(nil)), n, nil
}

// list issues a Depth-1 PROPFIND and parses the multistatus XML.
func (p webDAV) list() ([]CloudFile, error) {
	if p.cfg.URL == "" {
		return nil, fmt.Errorf("no destination URL configured")
	}
	req, err := http.NewRequest("PROPFIND", p.cfg.URL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Depth", "1")
	req.Header.Set("Content-Type", "text/xml")
	applyBasicAuth(req, p.cfg.Username, p.cfg.Password)

	resp, err := p.s.httpClient().Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("WebDAV list returned HTTP %d", resp.StatusCode)
	}

	var ms struct {
		Responses []struct {
			Href  string `xml:"href"`
			Props []struct {
				Length   string `xml:"prop>getcontentlength"`
				Modified string `xml:"prop>getlastmodified"`
			} `xml:"propstat"`
		} `xml:"response"`
	}
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if err := xml.Unmarshal(raw, &ms); err != nil {
		return nil, fmt.Errorf("parse WebDAV multistatus: %w", err)
	}

	baseName := path.Base(strings.TrimSuffix(p.cfg.URL, "/"))
	var files []CloudFile
	for _, r := range ms.Responses {
		href, err := url.PathUnescape(strings.TrimSpace(r.Href))
		if err != nil {
			href = r.Href
		}
		name := path.Base(strings.TrimSuffix(href, "/"))
		if name == "" || name == baseName {
			continue
		}
		f := CloudFile{Name: name, CreatedTime: time.Now().UTC().Format(time.RFC3339)}
		for _, prop := range r.Props {
			if prop.Length != "" {
				f.SizeBytes, _ = strconv.ParseInt(strings.TrimSpace(prop.Length), 10, 64)
			}
			if prop.Modified != "" {
				if t, err := time.Parse(time.RFC1123, strings.TrimSpace(prop.Modified)); err == nil {
					f.CreatedTime = t.UTC().Format(time.RFC3339)
				}
			}
		}
		files = append(files, f)
	}
	return files, nil
}

func (p webDAV) download(fileName, localPath string) error {
	req, err := http.NewRequest(http.MethodGet, joinURL(p.cfg.URL, url.PathEscape(fileName)), nil)
	if err != nil {
		return err
	}
	applyBasicAuth(req, p.cfg.Username, p.cfg.Password)
	if err := p.s.fetchToFile(req, localPath); err != nil {
		return fmt.Errorf("WebDAV: %w", err)
	}
	return nil
}

func (p webDAV) remove(f CloudFile) error {
	req, err := http.NewRequest(http.MethodDelete, joinURL(p.cfg.URL, url.PathEscape(f.Name)), nil)
	if err != nil {
		return err
	}
	applyCustomHeaders(req, p.cfg.HeadersJSON)
	applyBasicAuth(req, p.cfg.Username, p.cfg.Password)
	return p.s.doOK(req)
}
