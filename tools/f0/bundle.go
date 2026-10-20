package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
)

type PortableEv struct {
	ID           string   `json:"id"`
	Kind         string   `json:"kind"`
	SHA          string   `json:"sha"`
	TreeSHA      string   `json:"tree_sha,omitempty"`
	Cmd          []string `json:"cmd"`
	ItemsChecked int      `json:"items_checked"`
	Status       string   `json:"status"`
}

type PortableBundle struct {
	Version      int               `json:"v"`
	HeadSHA      string            `json:"head_sha"`
	TreeSHA      string            `json:"tree_sha,omitempty"`
	MandateID    string            `json:"mandate_id,omitempty"`
	MandateTier  string            `json:"mandate_tier,omitempty"`
	Evidence     []PortableEv      `json:"evidence"`
	Blobs        map[string]string `json:"blobs,omitempty"`
	BundleSHA256 string            `json:"bundle_sha256"`
}

type bundleWithoutHash struct {
	Version     int               `json:"v"`
	HeadSHA     string            `json:"head_sha"`
	TreeSHA     string            `json:"tree_sha,omitempty"`
	MandateID   string            `json:"mandate_id,omitempty"`
	MandateTier string            `json:"mandate_tier,omitempty"`
	Evidence    []PortableEv      `json:"evidence"`
	Blobs       map[string]string `json:"blobs,omitempty"`
}

func shaMatches(a, b string) bool {
	if a == "" || b == "" {
		return false
	}
	return strings.HasPrefix(a, b) || strings.HasPrefix(b, a)
}

func ComputeBundleHash(b *PortableBundle) (string, error) {
	evidence := b.Evidence
	if evidence == nil {
		evidence = []PortableEv{}
	}
	raw := bundleWithoutHash{
		Version:     b.Version,
		HeadSHA:     b.HeadSHA,
		TreeSHA:     b.TreeSHA,
		MandateID:   b.MandateID,
		MandateTier: b.MandateTier,
		Evidence:    evidence,
		Blobs:       b.Blobs,
	}
	data, err := json.Marshal(raw)
	if err != nil {
		return "", fmt.Errorf("canonical json marshal bundle: %w", err)
	}
	h := sha256.Sum256(data)
	return hex.EncodeToString(h[:]), nil
}

var bundleRegex = regexp.MustCompile(`(?s)<!--\s*f0:bundle:\s*(\{.*?\})\s*-->`)

func ParsePortableBundle(text string) (*PortableBundle, error) {
	match := bundleRegex.FindStringSubmatch(text)
	if len(match) < 2 {
		return nil, errors.New("no f0:bundle tag found in text")
	}
	var b PortableBundle
	if err := json.Unmarshal([]byte(match[1]), &b); err != nil {
		return nil, fmt.Errorf("malformed f0:bundle json: %w", err)
	}
	expectedHash, err := ComputeBundleHash(&b)
	if err != nil {
		return nil, fmt.Errorf("computing bundle hash: %w", err)
	}
	if b.BundleSHA256 != expectedHash {
		return nil, fmt.Errorf("bundle hash mismatch: got %s, expected %s (tampered)", b.BundleSHA256, expectedHash)
	}
	return &b, nil
}
