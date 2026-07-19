package fetch

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// AllowPrivateSync reports whether loopback / RFC1918 sync + import targets are
// permitted. It is OFF by default; set RED_ALLOW_PRIVATE_SYNC=true ONLY for
// local-dev federation testing on a trusted LAN (e.g. two nodes at
// 192.168.x.y:<port>). The cloud-metadata range (169.254.0.0/16) and multicast
// stay blocked regardless, so this never re-opens the metadata-SSRF hole.
func AllowPrivateSync() bool {
	return os.Getenv("RED_ALLOW_PRIVATE_SYNC") == "true"
}

// SafeClient creates an HTTP client that mitigates DNS Rebinding SSRF
func SafeClient() *http.Client {
	return &http.Client{
		Timeout: 15 * time.Second,
		Transport: &http.Transport{
			DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
				host, port, err := net.SplitHostPort(addr)
				if err != nil {
					return nil, err
				}
				ips, err := net.DefaultResolver.LookupIPAddr(ctx, host)
				if err != nil || len(ips) == 0 {
					return nil, fmt.Errorf("dns lookup failed")
				}

				for _, ip := range ips {
					// Link-local (incl. the 169.254.169.254 cloud-metadata range) and
					// multicast are ALWAYS blocked, even in local-dev mode.
					if ip.IP.IsLinkLocalUnicast() || ip.IP.IsMulticast() {
						return nil, fmt.Errorf("SSRF Blocked: forbidden IP %s", ip.IP)
					}
					// Loopback / RFC1918 / unspecified are blocked unless local-dev
					// federation testing is explicitly enabled (see AllowPrivateSync).
					if !AllowPrivateSync() && (ip.IP.IsLoopback() || ip.IP.IsPrivate() || ip.IP.IsUnspecified()) {
						return nil, fmt.Errorf("SSRF Blocked: forbidden IP %s", ip.IP)
					}
				}
				safeAddr := net.JoinHostPort(ips[0].IP.String(), port)
				return (&net.Dialer{Timeout: 5 * time.Second}).DialContext(ctx, network, safeAddr)
			},
		},
	}
}

func Pull(url, srcType, destDir string) error {
	// --- NEW: Native Git Intercept ---
	if srcType == "git" {
		_, err := pullGit(url, destDir)
		return err
	}

	if srcType == "raw" {
		if err := os.MkdirAll(filepath.Dir(destDir), 0755); err != nil {
			return err
		}
		if !strings.HasSuffix(strings.ToLower(destDir), ".md") {
			destDir += ".md"
		}
		resp, err := SafeClient().Get(url)
		if err != nil {
			return err
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			return fmt.Errorf("fetch: status %d", resp.StatusCode)
		}
		outFile, err := os.Create(destDir)
		if err != nil {
			return err
		}
		defer outFile.Close()
		_, err = io.Copy(outFile, io.LimitReader(resp.Body, 10*1024*1024)) // 10MB Limit
		return err
	}
	// ---------------------------------

	resp, err := SafeClient().Get(url)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("fetch: status %d", resp.StatusCode)
	}

	tmp, err := os.CreateTemp("", "red-*.tmp")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())

	if _, err = io.Copy(tmp, io.LimitReader(resp.Body, 100*1024*1024)); err != nil { // 100MB Limit
		return err
	}
	tmp.Close()

	switch srcType {
	case "tar.gz", "tgz":
		return extractTarGz(tmp.Name(), destDir)
	case "zip":
		return extractZip(tmp.Name(), destDir)
	default:
		return fmt.Errorf("fetch: unsupported type %q", srcType)
	}
}

func PullDelta(url, srcType, destDir string) ([]string, error) {
	if srcType == "git" {
		return pullGit(url, destDir)
	}
	err := Pull(url, srcType, destDir)
	return nil, err
}

func extractTarGz(src, dest string) error {
	f, err := os.Open(src)
	if err != nil {
		return err
	}
	defer f.Close()

	gz, err := gzip.NewReader(f)
	if err != nil {
		return err
	}
	defer gz.Close()

	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
		if err = writeEntry(dest, hdr.Name, hdr.FileInfo().IsDir(), tr); err != nil {
			return err
		}
	}
	return nil
}

func extractZip(src, dest string) error {
	r, err := zip.OpenReader(src)
	if err != nil {
		return err
	}
	defer r.Close()

	for _, f := range r.File {
		rc, err := f.Open()
		if err != nil {
			return err
		}
		err = writeEntry(dest, f.Name, f.FileInfo().IsDir(), rc)
		rc.Close()
		if err != nil {
			return err
		}
	}
	return nil
}

func writeEntry(dest, name string, isDir bool, r io.Reader) error {
	rel := strings.TrimPrefix(filepath.ToSlash(filepath.Clean(name)), "/")
	if rel == "" || strings.HasPrefix(rel, "..") {
		return nil // Block traversal
	}

	target := filepath.Join(dest, filepath.FromSlash(rel))

	if isDir {
		return os.MkdirAll(target, 0755)
	}

	if err := os.MkdirAll(filepath.Dir(target), 0755); err != nil {
		return err
	}

	// Read the entry into memory (bounded by the same 100MB limit as before) and
	// route it through writeIfChanged rather than os.Create. os.Create always
	// truncates, so re-extracting an archive blew away whatever was on disk —
	// bypassing both the SHA256 idempotency check (causing needless mtime churn /
	// watcher retriggers) and W1's signed-note protection. Going through
	// writeIfChanged means an archived, unsigned export can no longer clobber a
	// signed note, and an older signed copy cannot roll back a newer one.
	content, err := io.ReadAll(io.LimitReader(r, 100*1024*1024)) // 100MB Extracted File Limit
	if err != nil {
		return err
	}
	return writeIfChanged(target, content)
}
