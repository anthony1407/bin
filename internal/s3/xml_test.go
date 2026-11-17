package s3

import (
	"net/http/httptest"
	"strings"
	"testing"
)

// Request bodies are accepted with or without the S3 namespace, as AWS does:
// minio-go (so mc) and older SDKs leave it out.
func TestReadXMLBodyNamespace(t *testing.T) {
	for _, body := range []string{
		`<VersioningConfiguration><Status>Enabled</Status></VersioningConfiguration>`,
		`<VersioningConfiguration xmlns="http://s3.amazonaws.com/doc/2006-03-01/"><Status>Enabled</Status></VersioningConfiguration>`,
	} {
		var v versioningConfiguration
		if _, err := readXMLBody(httptest.NewRequest("PUT", "/b?versioning", strings.NewReader(body)), &v); err != nil || v.Status != "Enabled" {
			t.Errorf("%s: %+v %v", body, v, err)
		}
	}
	var tg tagging
	body := `<Tagging><TagSet><Tag><Key>team</Key><Value>media</Value></Tag></TagSet></Tagging>`
	if _, err := readXMLBody(httptest.NewRequest("PUT", "/b?tagging", strings.NewReader(body)), &tg); err != nil ||
		len(tg.TagSet.Tag) != 1 || tg.TagSet.Tag[0].Value != "media" {
		t.Errorf("tagging without namespace: %+v %v", tg, err)
	}
	// A body of another type, or another namespace, is still refused.
	var v versioningConfiguration
	for _, bad := range []string{`<Tagging><TagSet/></Tagging>`, `<VersioningConfiguration xmlns="urn:other"><Status>Enabled</Status></VersioningConfiguration>`} {
		if _, err := readXMLBody(httptest.NewRequest("PUT", "/b?versioning", strings.NewReader(bad)), &v); err == nil {
			t.Errorf("accepted %s", bad)
		}
	}
}
