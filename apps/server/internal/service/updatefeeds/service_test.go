package updatefeeds

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"strings"
	"testing"

	"github.com/opennavo/opennavo/server/internal/homebrew"
	"github.com/stretchr/testify/require"
)

const fixtureFeed = `<rss xmlns:s="http://www.andymatuschak.org/xml-namespaces/sparkle"><channel><item><s:version>2</s:version><enclosure url="https://example.org/app.zip"/></item></channel></rss>`

func testArchive(t *testing.T) []byte {
	t.Helper()
	var buffer bytes.Buffer
	zip := gzip.NewWriter(&buffer)
	archive := tar.NewWriter(zip)
	body := []byte("cask \"fixture\" do\nlivecheck do\nurl \"https://example.org/feed.xml\"\nstrategy :sparkle\nend\nend")
	require.NoError(t, archive.WriteHeader(&tar.Header{Name: "homebrew-cask-main/Casks/f/fixture.rb", Mode: 0600, Size: int64(len(body)), Typeflag: tar.TypeReg}))
	_, err := archive.Write(body)
	require.NoError(t, err)
	require.NoError(t, archive.Close())
	require.NoError(t, zip.Close())
	return buffer.Bytes()
}
func TestArchiveReadsOnlyStaticMetadataAndSparkleValidationIsBounded(t *testing.T) {
	feeds, err := parseArchive(testArchive(t))
	require.NoError(t, err)
	require.Equal(t, map[string]homebrew.UpdateLivecheck{"fixture": {Source: "sparkle", URL: "https://example.org/feed.xml"}}, feeds)
	require.NoError(t, ValidateSparkle([]byte(fixtureFeed)))
	for _, body := range []string{`<!DOCTYPE rss [<!ENTITY x SYSTEM "file:///etc/passwd">]><rss/>`, `<html>not appcast</html>`, string(bytes.Repeat([]byte{'x'}, (2<<20)+1)), `<rss><item><version>2</version><enclosure url="https://example.org/a"/></item></rss>`, `<rss xmlns:s="http://www.andymatuschak.org/xml-namespaces/sparkle"><item><s:version>2</s:version><enclosure url="http://example.org/app.zip"/></item></rss>`} {
		require.Error(t, ValidateSparkle([]byte(body)))
	}
}

func TestInformationalFeedRequiresAnHTTPSDestination(t *testing.T) {
	for _, link := range []string{"", "http://example.org/upgrade", "https://example.org/upgrade"} {
		body := `<rss xmlns:s="http://www.andymatuschak.org/xml-namespaces/sparkle"><channel><item><s:version>2</s:version><s:informationalUpdate/><link>` + link + `</link></item></channel></rss>`
		err := ValidateSparkle([]byte(body))
		if strings.HasPrefix(link, "https:") {
			require.NoError(t, err)
		} else {
			require.Error(t, err)
		}
	}
	require.Error(t, ValidateSparkle([]byte(strings.ReplaceAll(fixtureFeed, "rss", "html"))))
}
