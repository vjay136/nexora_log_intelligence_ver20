package controlplane

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"strings"
	"testing"

	metastore "nexora.local/nli-v20/internal/store"
)

type uploadSupabaseMetadata struct{ metastore.Store }

func (uploadSupabaseMetadata) Mode() string { return "supabase" }

type uploadBlobObject struct {
	bucket string
	data   []byte
}
type uploadBlobStub struct {
	objects       map[string]uploadBlobObject
	puts, deletes int
}

func (b *uploadBlobStub) Mode() string { return "supabase-private-storage" }
func (b *uploadBlobStub) PutVerified(_ context.Context, bucket, path string, reader io.Reader, size int64, mime, expected string) error {
	data, err := io.ReadAll(reader)
	if err != nil {
		return err
	}
	if int64(len(data)) != size {
		return io.ErrUnexpectedEOF
	}
	sum := sha256.Sum256(data)
	if hex.EncodeToString(sum[:]) != expected {
		return errors.New("digest mismatch")
	}
	if bucket == "nli-rollout-artifacts" && strings.HasPrefix(path, "parser/") {
		return errors.New("parser file purpose forbidden")
	}
	b.objects[bucket+"/"+path] = uploadBlobObject{bucket: bucket, data: data}
	b.puts++
	return nil
}
func (b *uploadBlobStub) Open(_ context.Context, bucket, path string) (io.ReadCloser, int64, error) {
	object, ok := b.objects[bucket+"/"+path]
	if !ok {
		return nil, 0, os.ErrNotExist
	}
	return io.NopCloser(bytes.NewReader(object.data)), int64(len(object.data)), nil
}
func (b *uploadBlobStub) Delete(_ context.Context, bucket, path string) error {
	delete(b.objects, bucket+"/"+path)
	b.deletes++
	return nil
}

func multipartUpload(t *testing.T, fixture *apiFixture, filename string, content []byte, retain bool) (*http.Response, []byte) {
	t.Helper()
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, err := writer.CreateFormFile("file", filename)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := part.Write(content); err != nil {
		t.Fatal(err)
	}
	if err := writer.WriteField("service", "upload-test"); err != nil {
		t.Fatal(err)
	}
	if err := writer.WriteField("retain", map[bool]string{true: "true", false: "false"}[retain]); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	request, err := http.NewRequest(http.MethodPost, fixture.http.URL+"/api/v1/assurance/upload", &body)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", writer.FormDataContentType())
	request.Header.Set("X-CSRF-Token", fixture.csrf)
	response, err := fixture.client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	data, err := io.ReadAll(response.Body)
	response.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	return response, data
}

func TestAssuranceUploadTransientAndEncryptedRetention(t *testing.T) {
	f := newAPIFixture(t)
	fixtureText := "upload-secret-fixture-value level=info request_id=req-1\n"
	response, data := multipartUpload(t, f, "fixture.log", []byte(fixtureText), false)
	if response.StatusCode != http.StatusOK {
		t.Fatalf("transient upload %d: %s", response.StatusCode, data)
	}
	if bytes.Contains(data, []byte(fixtureText)) {
		t.Fatalf("raw fixture echoed in upload response: %s", data)
	}
	response, data = multipartUpload(t, f, "fixture.log", []byte(fixtureText), true)
	if response.StatusCode != http.StatusOK {
		t.Fatalf("retained upload %d: %s", response.StatusCode, data)
	}
	result := decodeMap(t, data)
	items := result["results"].([]any)
	id := items[0].(map[string]any)["retained_id"].(string)
	doc, err := f.server.metadata.Get(context.Background(), "assurance-cases", id, 0)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(doc.Payload, []byte(fixtureText)) {
		t.Fatalf("plaintext in metadata payload: %s", doc.Payload)
	}
	var sealed sealedAssuranceCase
	if err := json.Unmarshal(doc.Payload, &sealed); err != nil {
		t.Fatal(err)
	}
	if sealed.SHA256 == "" || sealed.ExpandedBytes != int64(len(fixtureText)) || sealed.Ciphertext == "" || sealed.OriginalFilename != "fixture.log" {
		t.Fatalf("retained metadata incomplete: %+v", sealed)
	}
	getResponse, getData := f.request(http.MethodGet, "/api/v1/assurance/cases/"+id, nil)
	retrieved := decodeMap(t, getData)
	retrievedFixture, _ := retrieved["fixture"].(map[string]any)
	if getResponse.StatusCode != http.StatusOK || retrievedFixture["raw"] != fixtureText {
		t.Fatalf("authenticated fixture retrieval failed (%d): %s", getResponse.StatusCode, getData)
	}
}

func zipBytes(t *testing.T, name string, data []byte) []byte {
	t.Helper()
	var b bytes.Buffer
	w := zip.NewWriter(&b)
	part, err := w.Create(name)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = part.Write(data)
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

func tarBytes(t *testing.T, name string, data []byte) []byte {
	t.Helper()
	var b bytes.Buffer
	w := tar.NewWriter(&b)
	if err := w.WriteHeader(&tar.Header{Name: name, Mode: 0600, Size: int64(len(data)), Typeflag: tar.TypeReg}); err != nil {
		t.Fatal(err)
	}
	_, _ = w.Write(data)
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

func TestAssuranceUploadRejectsTraversalMalformedAndExpansionBombs(t *testing.T) {
	for _, test := range []struct {
		name string
		body []byte
	}{
		{"slip.zip", zipBytes(t, "../../escape.log", []byte("x=1\n"))},
		{"slip.tar", tarBytes(t, "../escape.log", []byte("x=1\n"))},
		{"malformed.zip", []byte("PK\x03\x04broken")},
		{"bomb.gz", func() []byte {
			var b bytes.Buffer
			w := gzip.NewWriter(&b)
			_, _ = w.Write(bytes.Repeat([]byte("x"), 700<<10))
			_ = w.Close()
			return b.Bytes()
		}()},
		{"oversize.log", bytes.Repeat([]byte("x"), assuranceUploadMaxCompressed+1)},
		{"symlink.zip", zipSymlinkBytes(t)},
	} {
		f := newAPIFixture(t)
		response, data := multipartUpload(t, f, test.name, test.body, false)
		if response.StatusCode < 400 {
			t.Errorf("%s accepted: %s", test.name, data)
		}
	}
}

func zipSymlinkBytes(t *testing.T) []byte {
	t.Helper()
	var b bytes.Buffer
	w := zip.NewWriter(&b)
	header := &zip.FileHeader{Name: "link.log", Method: zip.Store}
	header.SetMode(os.ModeSymlink | 0600)
	part, err := w.CreateHeader(header)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = part.Write([]byte("/etc/passwd"))
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

func TestAssuranceUploadReturnsSafeArchiveMemberMetadata(t *testing.T) {
	f := newAPIFixture(t)
	response, data := multipartUpload(t, f, "bundle.zip", zipBytes(t, "logs/app.log", []byte("level=info user=alice\n")), false)
	if response.StatusCode != http.StatusOK {
		t.Fatalf("archive upload %d: %s", response.StatusCode, data)
	}
	root := decodeMap(t, data)
	members := root["results"].([]any)
	item := members[0].(map[string]any)
	if item["filename"] != "app.log" || item["archive_type"] != "zip" {
		t.Fatalf("unsafe or incomplete archive member descriptor: %s", data)
	}
}

func TestAssuranceUploadRequiresCSRFAndRejectsArchiveSymlinks(t *testing.T) {
	f := newAPIFixture(t)
	var archive bytes.Buffer
	w := tar.NewWriter(&archive)
	if err := w.WriteHeader(&tar.Header{Name: "link.log", Typeflag: tar.TypeSymlink, Linkname: "/etc/passwd"}); err != nil {
		t.Fatal(err)
	}
	_ = w.Close()
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	p, _ := mw.CreateFormFile("file", "links.tar")
	_, _ = p.Write(archive.Bytes())
	_ = mw.Close()
	req, err := http.NewRequest(http.MethodPost, f.http.URL+"/api/v1/assurance/upload", bytes.NewReader(body.Bytes()))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", mw.FormDataContentType())
	response, err := f.client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	payload, _ := io.ReadAll(response.Body)
	response.Body.Close()
	if response.StatusCode == http.StatusOK {
		t.Fatalf("upload without CSRF accepted: %s", payload)
	}
	response2, data := multipartUpload(t, f, "links.tar", archive.Bytes(), false)
	if response2.StatusCode < 400 {
		t.Fatalf("tar symlink accepted: %s", data)
	}
}

func TestRolloutFilePurposeAllowlistExcludesParserAssets(t *testing.T) {
	if artifactLimit("parser", 512<<20) != 0 {
		t.Fatal("parser assets are still accepted as rollout file artifacts")
	}
	if artifactLimit("rpm", 512<<20) <= 0 || artifactLimit("config", 512<<20) <= 0 {
		t.Fatal("approved rollout RPM/config purposes were disabled")
	}
}

func TestSupabaseAssuranceFilesUseEncryptedFixtureBucketOnly(t *testing.T) {
	f := newAPIFixture(t)
	stub := &uploadBlobStub{objects: map[string]uploadBlobObject{}}
	f.server.metadata = uploadSupabaseMetadata{Store: f.server.metadata}
	f.server.assurance.store = f.server.metadata
	f.server.artifactStorage = stub
	raw := "supabase-fixture-secret user=alice@example.com\n"
	response, data := multipartUpload(t, f, "fixture.log", []byte(raw), false)
	if response.StatusCode != http.StatusOK {
		t.Fatalf("transient upload %d %s", response.StatusCode, data)
	}
	if stub.puts != 0 {
		t.Fatal("transient production-like parse stored a file")
	}
	response, data = multipartUpload(t, f, "fixture.log", []byte(raw), true)
	if response.StatusCode != http.StatusOK {
		t.Fatalf("retained upload %d %s", response.StatusCode, data)
	}
	if stub.puts != 1 {
		t.Fatalf("fixture upload Storage puts=%d", stub.puts)
	}
	for key, obj := range stub.objects {
		if obj.bucket != "nli-assurance-fixtures" || !strings.HasSuffix(key, ".enc") || bytes.Contains(obj.data, []byte(raw)) {
			t.Fatalf("invalid Assurance object %q", key)
		}
	}
	item := decodeMap(t, data)["results"].([]any)[0].(map[string]any)
	id := item["retained_id"].(string)
	doc, err := f.server.metadata.Get(context.Background(), "assurance-cases", id, 0)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(doc.Payload, []byte(raw)) {
		t.Fatal("raw fixture appeared in Supabase metadata")
	}
	var sealed sealedAssuranceCase
	if err := json.Unmarshal(doc.Payload, &sealed); err != nil {
		t.Fatal(err)
	}
	if sealed.Ciphertext != "" || sealed.CiphertextObjectPath == "" || sealed.CiphertextStorageBackend != "supabase-private-storage" {
		t.Fatalf("Supabase descriptor retained inline ciphertext: %+v", sealed)
	}
	getResponse, getData := f.request(http.MethodGet, "/api/v1/assurance/cases/"+id, nil)
	retrieved := decodeMap(t, getData)
	retrievedFixture, _ := retrieved["fixture"].(map[string]any)
	if getResponse.StatusCode != http.StatusOK || retrievedFixture["raw"] != raw {
		t.Fatalf("authenticated fixture retrieval failed: %d %s", getResponse.StatusCode, getData)
	}
	deleteResponse, deleteData := f.request(http.MethodDelete, "/api/v1/assurance/cases/"+id, map[string]any{})
	if deleteResponse.StatusCode != http.StatusOK {
		t.Fatalf("fixture purge failed: %d %s", deleteResponse.StatusCode, deleteData)
	}
	if stub.deletes != 1 {
		t.Fatalf("fixture object was not purged: %+v", stub)
	}
	// The classic paste-and-save Assurance flow uses the same private fixture bucket.
	savedResponse, savedData := f.request(http.MethodPost, "/api/v1/assurance/cases", map[string]any{"name": "manual fixture", "retain": true, "case": map[string]any{"raw": raw, "service": "mail"}})
	if savedResponse.StatusCode != http.StatusCreated {
		t.Fatalf("manual Assurance save failed: %d %s", savedResponse.StatusCode, savedData)
	}
	if stub.puts != 2 {
		t.Fatalf("manual Assurance save did not use encrypted fixture bucket: %d", stub.puts)
	}
}
