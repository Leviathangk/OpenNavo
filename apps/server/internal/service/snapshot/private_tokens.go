package snapshot

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"fmt"
	"io"
)

// bundleTokenList is the part of every bundle document the revocation self-check reads: the base
// snapshot and each language text pack list their items, and a private package must appear in none
// of them.
type bundleTokenList struct {
	Items []struct {
		Token string `json:"token"`
	} `json:"items"`
}

// bundleContainsToken reports the exact token of a private package that survives in the new bundle.
// It parses the documents instead of scanning their bytes: a private token is often a prefix of a
// public one (1password and 1password-cli, ray and raycast, obs and obsidian) or appears in ordinary
// copy, and a byte scan would refuse to acknowledge a revocation forever. Privacy applies to one
// catalog token at a time, exactly as the directory filter does: a private base token does not cover
// its variants (`aerial` private leaves `aerial@beta` public), and a private variant covers only
// itself. A file that cannot be decompressed or decoded fails closed with its key, because a bundle
// that cannot be checked is not a bundle that can be acknowledged.
func bundleContainsToken(bundle Bundle, tokens []string) (string, bool) {
	if len(tokens) == 0 {
		return "", false
	}
	wanted := make(map[string]string, len(tokens))
	for _, token := range tokens {
		wanted[token] = token
	}
	for _, file := range bundle.Files {
		var document bundleTokenList
		if err := decodeGzipJSON(file.Data, &document); err != nil {
			return file.Key, true
		}
		for _, item := range document.Items {
			// The comparison is the whole token: `aerial@beta` is a directory entry of its own, so a
			// private `aerial` does not hide it and a private variant does not hide the base entry.
			if original, ok := wanted[item.Token]; ok {
				return original, true
			}
		}
	}
	return "", false
}

func decodeGzipJSON(data []byte, out any) error {
	if !bytes.HasPrefix(data, []byte{0x1f, 0x8b}) {
		return fmt.Errorf("bundle file is not gzip: %x", data[:min(len(data), 2)])
	}
	reader, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		return err
	}
	raw, err := io.ReadAll(reader)
	if closeErr := reader.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	return json.Unmarshal(raw, out)
}

// bundleDocumentTokens returns every package token a bundle file publishes; it is the test-side
// counterpart of bundleContainsToken and shares its decoder, so a test cannot pass on a format the
// self-check reads differently.
func bundleDocumentTokens(file File) ([]string, error) {
	var document bundleTokenList
	if err := decodeGzipJSON(file.Data, &document); err != nil {
		return nil, err
	}
	tokens := make([]string, 0, len(document.Items))
	for _, item := range document.Items {
		tokens = append(tokens, item.Token)
	}
	return tokens, nil
}
