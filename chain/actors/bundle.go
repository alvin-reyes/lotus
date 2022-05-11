package actors

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/xerrors"

	logging "github.com/ipfs/go-log/v2"
)

var logb = logging.Logger("bundle-fetcher")

type BundleFetcher struct {
	path string
}

func NewBundleFetcher(basepath string) (*BundleFetcher, error) {
	path := filepath.Join(basepath, "builtin-actors")
	if err := os.MkdirAll(path, 0755); err != nil {
		return nil, xerrors.Errorf("error making bundle directory %s: %w", path, err)
	}

	return &BundleFetcher{path: path}, nil
}

func (b *BundleFetcher) Fetch(version Version, release, netw string) (path string, err error) {
	bundleName := fmt.Sprintf("builtin-actors-%s", netw)
	bundleFile := fmt.Sprintf("%s.car", bundleName)
	bundleHash := fmt.Sprintf("%s.sha256", bundleName)
	bundleBasePath := filepath.Join(b.path, fmt.Sprintf("v%d", version), release)

	if err := os.MkdirAll(bundleBasePath, 0755); err != nil {
		return "", xerrors.Errorf("error making bundle directory %s: %w", bundleBasePath, err)
	}

	// check if it exists; if it does, check the hash
	bundleFilePath := filepath.Join(bundleBasePath, bundleFile)
	if _, err := os.Stat(bundleFilePath); err == nil {
		err := b.check(bundleBasePath, bundleFile, bundleHash)
		if err == nil {
			return bundleFilePath, nil
		}

		logb.Warnf("invalid bundle %s: %s; refetching", bundleName, err)
	}

	logb.Infof("fetching bundle %s", bundleFile)
	if err := b.fetch(release, bundleBasePath, bundleFile, bundleHash); err != nil {
		logb.Errorf("error fetching bundle %s: %s", bundleName, err)
		return "", xerrors.Errorf("error fetching bundle: %w", err)
	}

	if err := b.check(bundleBasePath, bundleFile, bundleHash); err != nil {
		logb.Errorf("error checking bundle %s: %s", bundleName, err)
		return "", xerrors.Errorf("error checking bundle: %s", err)
	}

	return bundleFilePath, nil
}

func (b *BundleFetcher) fetchURL(url, path string) error {
	logb.Infof("fetching URL: %s", url)

	resp, err := http.Get(url) //nolint
	if err != nil {
		return xerrors.Errorf("error fetching %s: %w", url, err)
	}
	defer resp.Body.Close() //nolint

	if resp.StatusCode != http.StatusOK {
		return xerrors.Errorf("error fetching %s: http response status is %d", url, resp.StatusCode)
	}

	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0644)
	if err != nil {
		return xerrors.Errorf("error opening %s for writing: %w", path, err)
	}
	defer f.Close() //nolint

	if _, err := io.Copy(f, resp.Body); err != nil {
		return xerrors.Errorf("error writing %s: %w", path, err)
	}

	return nil
}

func (b *BundleFetcher) fetch(release, bundleBasePath, bundleFile, bundleHash string) error {
	bundleHashUrl := fmt.Sprintf("https://github.com/filecoin-project/builtin-actors/releases/download/%s/%s",
		release, bundleHash)
	bundleHashPath := filepath.Join(bundleBasePath, bundleHash)
	if err := b.fetchURL(bundleHashUrl, bundleHashPath); err != nil {
		return err
	}

	bundleFileUrl := fmt.Sprintf("https://github.com/filecoin-project/builtin-actors/releases/download/%s/%s",
		release, bundleFile)
	bundleFilePath := filepath.Join(bundleBasePath, bundleFile)
	if err := b.fetchURL(bundleFileUrl, bundleFilePath); err != nil {
		return err
	}

	return nil
}

func (b *BundleFetcher) check(bundleBasePath, bundleFile, bundleHash string) error {
	bundleHashPath := filepath.Join(bundleBasePath, bundleHash)
	f, err := os.Open(bundleHashPath)
	if err != nil {
		return xerrors.Errorf("error opening %s: %w", bundleHashPath, err)
	}
	defer f.Close() //nolint

	bs, err := io.ReadAll(f)
	if err != nil {
		return xerrors.Errorf("error reading %s: %w", bundleHashPath, err)
	}

	parts := strings.Split(string(bs), " ")
	hashHex := parts[0]
	expectedDigest, err := hex.DecodeString(hashHex)
	if err != nil {
		return xerrors.Errorf("error decoding digest from %s: %w", bundleHashPath, err)
	}

	bundleFilePath := filepath.Join(bundleBasePath, bundleFile)
	f, err = os.Open(bundleFilePath)
	if err != nil {
		return xerrors.Errorf("error opening %s: %w", bundleFilePath, err)
	}
	defer f.Close() //nolint

	h256 := sha256.New()
	if _, err := io.Copy(h256, f); err != nil {
		return xerrors.Errorf("error computing digest for %s: %w", bundleFilePath, err)
	}
	digest := h256.Sum(nil)

	if !bytes.Equal(digest, expectedDigest) {
		return xerrors.Errorf("hash mismatch")
	}

	return nil
}
