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
					if ip.IP.IsLoopback() || ip.IP.IsPrivate() || ip.IP.IsLinkLocalUnicast() || ip.IP.IsMulticast() || ip.IP.IsUnspecified() {
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

	out, err := os.Create(target)
	if err != nil {
		return err
	}
	defer out.Close()

	_, err = io.Copy(out, io.LimitReader(r, 100*1024*1024)) // 100MB Extracted File Limit
	return err
}
