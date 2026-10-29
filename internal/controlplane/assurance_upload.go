package controlplane

import (
	"archive/tar"
	"archive/zip"
	"bufio"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/csv"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"nexora.local/nli-v20/internal/auditlog"
	"nexora.local/nli-v20/internal/middleware"
	metastore "nexora.local/nli-v20/internal/store"
)

const (
	assuranceUploadMaxCompressed = 4 << 20
	assuranceUploadMaxExpanded   = 8 << 20
	assuranceUploadMaxMembers    = 32
	assuranceUploadMaxRecords    = 1000
	assuranceUploadMaxLineBytes  = 1 << 20
	assuranceUploadMaxRatio      = 200
	assuranceUploadMaxRetained   = 1 << 20
)

type uploadedMember struct {
	Name           string
	Content        []byte
	CompressedSize int64
	ArchiveType    string
}

type uploadRecord struct {
	Raw        string
	SourceLine int
}

func (s *Server) handleAssuranceUpload(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", "POST")
		jsonError(w, "POST required", http.StatusMethodNotAllowed)
		return
	}
	principal := s.authorize(w, r, middleware.PermissionParsePreview)
	if principal == nil {
		return
	}
	if !strings.HasPrefix(strings.ToLower(r.Header.Get("Content-Type")), "multipart/form-data;") {
		jsonError(w, "multipart/form-data with one file is required", http.StatusUnsupportedMediaType)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, assuranceUploadMaxCompressed+(64<<10))
	reader, err := r.MultipartReader()
	if err != nil {
		jsonError(w, "invalid multipart upload", http.StatusBadRequest)
		return
	}
	var filename, service string
	var retain bool
	var parserID string
	var upload []byte
	files := 0
	for {
		part, nextErr := reader.NextPart()
		if nextErr == io.EOF {
			break
		}
		if nextErr != nil {
			jsonError(w, "malformed multipart upload", http.StatusBadRequest)
			return
		}
		if part.FormName() == "file" {
			files++
			if files > 1 {
				part.Close()
				jsonError(w, "upload exactly one file or archive", http.StatusBadRequest)
				return
			}
			filename, err = safeFilename(part.FileName())
			if err != nil {
				part.Close()
				jsonError(w, "unsafe upload filename", http.StatusBadRequest)
				return
			}
			upload, err = io.ReadAll(io.LimitReader(part, assuranceUploadMaxCompressed+1))
			part.Close()
			if err != nil || len(upload) == 0 || len(upload) > assuranceUploadMaxCompressed {
				jsonError(w, "compressed upload must be between 1 byte and 4 MiB", http.StatusRequestEntityTooLarge)
				return
			}
		} else if part.FormName() == "service" {
			value, readErr := io.ReadAll(io.LimitReader(part, 129))
			part.Close()
			if readErr != nil || len(value) > 128 {
				jsonError(w, "service exceeds 128 bytes", http.StatusBadRequest)
				return
			}
			service = strings.TrimSpace(string(value))
		} else if part.FormName() == "retain" {
			value, readErr := io.ReadAll(io.LimitReader(part, 8))
			part.Close()
			if readErr != nil || (string(value) != "true" && string(value) != "false") {
				jsonError(w, "retain must be true or false", http.StatusBadRequest)
				return
			}
			retain = string(value) == "true"
		} else if part.FormName() == "parser_id" {
			value, readErr := io.ReadAll(io.LimitReader(part, 129))
			part.Close()
			if readErr != nil || len(value) > 128 {
				jsonError(w, "parser_id exceeds 128 bytes", http.StatusBadRequest)
				return
			}
			parserID = strings.TrimSpace(string(value))
		} else {
			part.Close()
			jsonError(w, "only file, service, parser_id and retain fields are accepted", http.StatusBadRequest)
			return
		}
	}
	if filename == "" || files != 1 {
		jsonError(w, "file field is required", http.StatusBadRequest)
		return
	}
	if service == "" {
		service = "auto"
	}
	if retain {
		principal = s.authorize(w, r, middleware.PermissionAssuranceSave)
		if principal == nil {
			return
		}
		if !s.assurance.enabled {
			jsonError(w, "Assurance fixture retention is disabled", http.StatusServiceUnavailable)
			return
		}
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	members, err := decodeUploadedMembers(filename, upload)
	if err != nil {
		jsonError(w, err.Error(), http.StatusBadRequest)
		return
	}
	type preparedMember struct {
		member    uploadedMember
		mediaType string
		records   []uploadRecord
	}
	prepared := make([]preparedMember, 0, len(members))
	totalRecords := 0
	for _, member := range members {
		if err := ctx.Err(); err != nil {
			jsonError(w, "upload parsing timed out", http.StatusRequestTimeout)
			return
		}
		records, mediaType, err := parseUploadedRecords(member.Name, member.Content)
		if err != nil {
			jsonError(w, fmt.Sprintf("%s: %s", member.Name, err.Error()), http.StatusBadRequest)
			return
		}
		totalRecords += len(records)
		if totalRecords > assuranceUploadMaxRecords {
			jsonError(w, "upload contains more than 1000 log records", http.StatusRequestEntityTooLarge)
			return
		}
		prepared = append(prepared, preparedMember{member: member, mediaType: mediaType, records: records})
	}
	memberResults := make([]map[string]any, 0, len(prepared))
	for _, preparedMember := range prepared {
		member, mediaType, records := preparedMember.member, preparedMember.mediaType, preparedMember.records
		if err := ctx.Err(); err != nil {
			jsonError(w, "upload parsing timed out", http.StatusRequestTimeout)
			return
		}
		recordResults := make([]map[string]any, 0, len(records))
		for index, record := range records {
			if err := ctx.Err(); err != nil {
				jsonError(w, "upload parsing timed out", http.StatusRequestTimeout)
				return
			}
			result := s.runAssurance(assuranceRequest{Raw: record.Raw, Service: service, Source: member.Name, ParserID: parserID})
			result["record_index"] = index + 1
			result["source_line"] = record.SourceLine
			recordResults = append(recordResults, result)
		}
		memberHash := hashBytes(member.Content)
		memberResult := map[string]any{"filename": path.Base(member.Name), "media_type": mediaType, "archive_type": member.ArchiveType, "compressed_bytes": member.CompressedSize, "expanded_bytes": len(member.Content), "sha256": hex.EncodeToString(memberHash), "service": service, "parser_id": parserID, "record_count": len(records), "results": recordResults, "retained": false}
		if retain {
			retainedID, revision, saveErr := s.retainUploadedMember(r.Context(), principal, member, mediaType, service, parserID, resultSummary(recordResults), memberHash)
			if saveErr != nil {
				jsonError(w, "encrypted fixture retention failed", http.StatusServiceUnavailable)
				return
			}
			memberResult["retained"] = true
			memberResult["retained_id"] = retainedID
			memberResult["retained_revision"] = revision
		}
		memberResults = append(memberResults, memberResult)
	}
	actor := principal.Username
	s.auditAction(principal, "assurance.upload", "assurance-upload/"+safeID(filename), "success", map[string]string{"members": fmt.Sprint(len(members)), "records": fmt.Sprint(totalRecords), "retained": fmt.Sprint(retain), "raw_retained": fmt.Sprint(retain)})
	jsonOK(w, map[string]any{"filename": filename, "service": service, "parser_id": parserID, "member_count": len(members), "record_count": totalRecords, "retained": retain, "results": memberResults, "actor": actor, "raw_samples_retained": retain, "limits": map[string]any{"compressed_bytes": assuranceUploadMaxCompressed, "expanded_bytes": assuranceUploadMaxExpanded, "members": assuranceUploadMaxMembers, "records": assuranceUploadMaxRecords, "expansion_ratio": assuranceUploadMaxRatio}})
}

func decodeUploadedMembers(filename string, data []byte) ([]uploadedMember, error) {
	extension := strings.ToLower(filepath.Ext(filename))
	switch extension {
	case ".zip":
		return decodeUploadedZip(data)
	case ".tar":
		return decodeUploadedTar(data)
	case ".gz":
		return decodeUploadedGzip(filename, data)
	case ".log", ".txt", ".json", ".jsonl", ".csv":
		if len(data) > assuranceUploadMaxExpanded {
			return nil, errors.New("expanded file exceeds 8 MiB")
		}
		return []uploadedMember{{Name: filename, Content: append([]byte(nil), data...), CompressedSize: int64(len(data)), ArchiveType: "none"}}, nil
	default:
		return nil, errors.New("supported uploads are .log, .txt, .json, .jsonl, .csv, .gz, .zip or .tar")
	}
}

func validateArchiveName(name string) (string, error) {
	if name == "" || strings.ContainsRune(name, '\x00') || strings.Contains(name, "\\") || strings.HasPrefix(name, "/") {
		return "", errors.New("archive contains an unsafe member path")
	}
	clean := path.Clean(name)
	if clean == "." || clean == ".." || strings.HasPrefix(clean, "../") || clean != name && name != "./"+clean {
		return "", errors.New("archive contains a traversal member path")
	}
	if _, err := safeFilename(path.Base(clean)); err != nil {
		return "", errors.New("archive contains an unsafe member name")
	}
	return path.Base(clean), nil
}

func decodeUploadedZip(data []byte) ([]uploadedMember, error) {
	reader, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return nil, errors.New("malformed ZIP archive")
	}
	if len(reader.File) > assuranceUploadMaxMembers {
		return nil, errors.New("ZIP archive has more than 32 members")
	}
	var expanded int64
	members := make([]uploadedMember, 0, len(reader.File))
	for _, file := range reader.File {
		if file.FileInfo().IsDir() {
			continue
		}
		name, err := validateArchiveName(file.Name)
		if err != nil {
			return nil, err
		}
		if file.Mode()&os.ModeSymlink != 0 || !file.Mode().IsRegular() || file.UncompressedSize64 > assuranceUploadMaxExpanded {
			return nil, errors.New("ZIP symlinks or oversized members are rejected")
		}
		if file.UncompressedSize64 > 0 && (file.CompressedSize64 == 0 || file.UncompressedSize64 > file.CompressedSize64*assuranceUploadMaxRatio) {
			return nil, errors.New("ZIP expansion ratio exceeds 200:1")
		}
		memberReader, err := file.Open()
		if err != nil {
			return nil, errors.New("malformed ZIP member")
		}
		content, readErr := io.ReadAll(io.LimitReader(memberReader, assuranceUploadMaxExpanded+1))
		closeErr := memberReader.Close()
		if readErr != nil || closeErr != nil || len(content) != int(file.UncompressedSize64) {
			return nil, errors.New("ZIP member is truncated or malformed")
		}
		expanded += int64(len(content))
		if expanded > assuranceUploadMaxExpanded {
			return nil, errors.New("ZIP expanded contents exceed 8 MiB")
		}
		members = append(members, uploadedMember{Name: name, Content: content, CompressedSize: int64(file.CompressedSize64), ArchiveType: "zip"})
	}
	return validateMemberCount(members)
}

func decodeUploadedTar(data []byte) ([]uploadedMember, error) {
	reader := tar.NewReader(bytes.NewReader(data))
	members := make([]uploadedMember, 0)
	var expanded int64
	for {
		header, err := reader.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, errors.New("malformed TAR archive")
		}
		if header.Typeflag == tar.TypeDir {
			continue
		}
		if header.Typeflag != tar.TypeReg && header.Typeflag != tar.TypeRegA {
			return nil, errors.New("TAR symlinks, links and special files are rejected")
		}
		name, err := validateArchiveName(header.Name)
		if err != nil {
			return nil, err
		}
		if header.Size < 0 || header.Size > assuranceUploadMaxExpanded {
			return nil, errors.New("TAR member exceeds 8 MiB")
		}
		content, err := io.ReadAll(io.LimitReader(reader, assuranceUploadMaxExpanded+1))
		if err != nil || int64(len(content)) != header.Size {
			return nil, errors.New("TAR member is truncated")
		}
		expanded += int64(len(content))
		if expanded > assuranceUploadMaxExpanded {
			return nil, errors.New("TAR expanded contents exceed 8 MiB")
		}
		members = append(members, uploadedMember{Name: name, Content: content, CompressedSize: header.Size, ArchiveType: "tar"})
		if len(members) > assuranceUploadMaxMembers {
			return nil, errors.New("TAR archive has more than 32 members")
		}
	}
	return validateMemberCount(members)
}

func decodeUploadedGzip(filename string, data []byte) ([]uploadedMember, error) {
	reader, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		return nil, errors.New("malformed gzip stream")
	}
	content, readErr := io.ReadAll(io.LimitReader(reader, assuranceUploadMaxExpanded+1))
	closeErr := reader.Close()
	if readErr != nil || closeErr != nil || len(content) > assuranceUploadMaxExpanded {
		return nil, errors.New("gzip expanded contents exceed 8 MiB or are malformed")
	}
	if len(content) > 0 && (len(data) == 0 || len(content) > len(data)*assuranceUploadMaxRatio) {
		return nil, errors.New("gzip expansion ratio exceeds 200:1")
	}
	name := strings.TrimSuffix(filename, filepath.Ext(filename))
	if strings.EqualFold(filepath.Ext(name), ".tar") {
		members, err := decodeUploadedTar(content)
		if err != nil {
			return nil, err
		}
		for index := range members {
			members[index].ArchiveType = "tar+gzip"
			members[index].CompressedSize = int64(len(data))
		}
		return members, nil
	}
	if name == filename || !supportedTextName(name) {
		name = name + ".txt"
	}
	return []uploadedMember{{Name: path.Base(name), Content: content, CompressedSize: int64(len(data)), ArchiveType: "gzip"}}, nil
}

func supportedTextName(name string) bool {
	switch strings.ToLower(filepath.Ext(name)) {
	case ".log", ".txt", ".json", ".jsonl", ".csv":
		return true
	default:
		return false
	}
}

func validateMemberCount(members []uploadedMember) ([]uploadedMember, error) {
	if len(members) == 0 {
		return nil, errors.New("archive contains no regular files")
	}
	if len(members) > assuranceUploadMaxMembers {
		return nil, errors.New("archive has more than 32 members")
	}
	for _, member := range members {
		if !supportedTextName(member.Name) {
			return nil, fmt.Errorf("unsupported archive member extension in %q", member.Name)
		}
	}
	return members, nil
}

func parseUploadedRecords(filename string, content []byte) ([]uploadRecord, string, error) {
	if len(content) > assuranceUploadMaxExpanded || !utf8.Valid(content) || bytes.IndexByte(content, 0) >= 0 {
		return nil, "", errors.New("member must be bounded UTF-8 text without NUL bytes")
	}
	extension := strings.ToLower(filepath.Ext(filename))
	mediaType := mime.TypeByExtension(extension)
	if mediaType == "" {
		mediaType = "text/plain; charset=utf-8"
	}
	if extension == ".json" {
		if len(content) > assuranceUploadMaxLineBytes {
			return nil, mediaType, errors.New("JSON document exceeds 1 MiB record limit")
		}
		if !json.Valid(content) {
			return nil, mediaType, errors.New("malformed JSON document")
		}
		return []uploadRecord{{Raw: string(content), SourceLine: 1}}, mediaType, nil
	}
	if extension == ".csv" {
		records, err := parseCSVRecords(content)
		return records, "text/csv; charset=utf-8", err
	}
	if extension == ".jsonl" {
		records, err := scanUploadLines(content, true)
		return records, "application/x-ndjson", err
	}
	records, err := scanUploadLines(content, false)
	return records, mediaType, err
}

func scanUploadLines(content []byte, validateJSON bool) ([]uploadRecord, error) {
	scanner := bufio.NewScanner(bytes.NewReader(content))
	scanner.Buffer(make([]byte, 64<<10), assuranceUploadMaxLineBytes)
	records := make([]uploadRecord, 0)
	lineNumber := 0
	for scanner.Scan() {
		lineNumber++
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		if validateJSON && !json.Valid([]byte(line)) {
			return nil, fmt.Errorf("invalid JSONL record at line %d", lineNumber)
		}
		records = append(records, uploadRecord{Raw: line, SourceLine: lineNumber})
		if len(records) > assuranceUploadMaxRecords {
			return nil, errors.New("member contains more than 1000 records")
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, errors.New("line exceeds 1 MiB or file is malformed")
	}
	return records, nil
}

func parseCSVRecords(content []byte) ([]uploadRecord, error) {
	reader := csv.NewReader(bytes.NewReader(content))
	reader.FieldsPerRecord = -1
	reader.ReuseRecord = true
	header, err := reader.Read()
	if err != nil {
		return nil, errors.New("CSV requires a header and at least one record")
	}
	if len(header) == 0 || len(header) > 256 {
		return nil, errors.New("CSV header must contain 1 to 256 columns")
	}
	seen := map[string]bool{}
	for index := range header {
		header[index] = strings.TrimSpace(header[index])
		if !knownCatalogKeyPattern.MatchString(header[index]) || seen[strings.ToLower(header[index])] {
			return nil, errors.New("CSV column names must be unique field identifiers")
		}
		seen[strings.ToLower(header[index])] = true
	}
	result := make([]uploadRecord, 0)
	line := 1
	for {
		row, err := reader.Read()
		if err == io.EOF {
			break
		}
		line++
		if err != nil || len(row) != len(header) {
			return nil, fmt.Errorf("malformed CSV row %d", line)
		}
		fields := make(map[string]string, len(header))
		for index, key := range header {
			fields[key] = row[index]
		}
		encoded, _ := json.Marshal(fields)
		result = append(result, uploadRecord{Raw: string(encoded), SourceLine: line})
		if len(result) > assuranceUploadMaxRecords {
			return nil, errors.New("CSV contains more than 1000 records")
		}
	}
	return result, nil
}

func resultSummary(results []map[string]any) map[string]any {
	parsed, quarantined, failures := 0, 0, 0
	parserIDs := map[string]bool{}
	for _, result := range results {
		if result["route"] == "normal" {
			parsed++
		} else if result["route"] == "quarantine" || result["route"] == "unknown" {
			quarantined++
		}
		if errs, ok := result["errors"].([]string); ok && len(errs) > 0 {
			failures++
		}
		if parser, ok := result["parser"].(map[string]any); ok {
			parserIDs[fmt.Sprint(parser["name"])] = true
		}
	}
	parsers := make([]string, 0, len(parserIDs))
	for id := range parserIDs {
		parsers = append(parsers, id)
	}
	sort.Strings(parsers)
	return map[string]any{"record_count": len(results), "parsed": parsed, "quarantined": quarantined, "errors": failures, "parsers": parsers}
}

func (s *Server) retainUploadedMember(ctx context.Context, principal *middleware.SecurityContext, member uploadedMember, mediaType, service, parserID string, summary map[string]any, digest []byte) (string, int, error) {
	if len(member.Content) > assuranceUploadMaxRetained {
		return "", 0, errors.New("retained member exceeds 1 MiB")
	}
	s.assurance.mu.Lock()
	defer s.assurance.mu.Unlock()
	documents, err := s.activeAssuranceCases(ctx)
	if err != nil {
		return "", 0, err
	}
	if len(documents) >= s.assurance.max {
		return "", 0, errors.New("Assurance fixture retention limit reached")
	}
	base := safeID(strings.TrimSuffix(path.Base(member.Name), filepath.Ext(member.Name)) + "-upload")
	if base == "" {
		base = "upload"
	}
	if len(base) > 72 {
		base = base[:72]
	}
	nonce := make([]byte, s.assurance.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", 0, err
	}
	id := base + "-" + hex.EncodeToString(digest[:6]) + "-" + hex.EncodeToString(nonce[:4])
	secret := assuranceSecret{Raw: string(member.Content), Desired: map[string]any{}, Actual: map[string]any{}, Service: service, Source: member.Name}
	plaintext, err := json.Marshal(secret)
	if err != nil {
		return "", 0, err
	}
	ciphertext := s.assurance.aead.Seal(nil, nonce, plaintext, []byte(id))
	expires := time.Time{}
	if !s.assurance.manual {
		expires = time.Now().UTC().Add(s.assurance.ttl)
	}
	storedCiphertext, objectPath, err := s.storeAssuranceCiphertext(ctx, ciphertext)
	if err != nil {
		return "", 0, err
	}
	if parserID == "" {
		parserID = "automatic"
	}
	sealed := sealedAssuranceCase{Name: path.Base(member.Name), Version: 1, ParserID: parserID, Passed: false, CreatedBy: principal.Username, CreatedAt: time.Now().UTC(), ExpiresAt: expires, Nonce: base64.StdEncoding.EncodeToString(nonce), Ciphertext: storedCiphertext, CiphertextObjectPath: objectPath, OriginalFilename: path.Base(member.Name), MediaType: mediaType, ArchiveType: member.ArchiveType, CompressedBytes: member.CompressedSize, ExpandedBytes: int64(len(member.Content)), SHA256: hex.EncodeToString(digest), Service: service, ParseSummary: summary}
	if objectPath != "" {
		cipherHash := hashBytes(ciphertext)
		sealed.CiphertextSHA256 = hex.EncodeToString(cipherHash[:])
		sealed.CiphertextStorageBackend = "supabase-private-storage"
	}
	payload, _ := json.Marshal(sealed)
	saved, err := s.metadata.Put(ctx, metastore.Document{Collection: "assurance-cases", ID: id, State: "retained", Payload: payload, CreatedBy: principal.Username})
	if err != nil {
		if sealed.CiphertextObjectPath != "" {
			_ = s.artifactStorage.Delete(ctx, "nli-assurance-fixtures", sealed.CiphertextObjectPath)
		}
		return "", 0, err
	}
	sealed.Version = saved.Revision
	_, _ = s.audit.Append(auditlog.Event{Action: "assurance.upload.retain", Actor: principal.Username, Role: principal.Role, Resource: "assurance-cases/" + id, Outcome: "success", Details: map[string]string{"revision": fmt.Sprint(saved.Revision), "sha256": sealed.SHA256}})
	return id, saved.Revision, nil
}
