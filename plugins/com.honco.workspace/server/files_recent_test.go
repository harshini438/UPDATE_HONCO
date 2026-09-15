package main

import "testing"

// fileKind is the only pure logic in the Files browser; the listing itself
// is a query and is covered by the API suite against real uploads.
func TestFileKind(t *testing.T) {
	cases := []struct {
		mime, ext, want string
	}{
		{"image/png", "png", "image"},
		{"image/jpeg", "JPG", "image"},
		{"video/mp4", "mp4", "video"},
		{"audio/mpeg", "mp3", "audio"},
		{"application/pdf", "pdf", "pdf"},
		// A recording arrives as video even when the extension is absent.
		{"video/mp4", "", "video"},
		// PDF recognised from the extension when the mime type is missing.
		{"", "pdf", "pdf"},
		{"", "docx", "document"},
		{"", "xlsx", "spreadsheet"},
		{"", "pptx", "presentation"},
		{"", "zip", "archive"},
		{"", "go", "code"},
		// A leading dot and stray case must not change the answer.
		{"", ".PY", "code"},
		{" ", " .Md ", "document"},
		// Unknown stays "file" rather than guessing.
		{"application/octet-stream", "bin", "file"},
		{"", "", "file"},
	}
	for _, c := range cases {
		if got := fileKind(c.mime, c.ext); got != c.want {
			t.Errorf("fileKind(%q, %q) = %q, want %q", c.mime, c.ext, got, c.want)
		}
	}
}

// An empty scope must mean "no files", never "every file". This is the
// difference between a user who is in no channel seeing nothing and
// seeing everything, so it is asserted rather than assumed.
func TestRecentFilesEmptyScopeReturnsNothing(t *testing.T) {
	s := &Store{}
	out, err := s.RecentFiles(nil, 25, 0)
	if err != nil {
		t.Fatalf("an empty scope must not error: %v", err)
	}
	if len(out) != 0 {
		t.Errorf("expected no rows for an empty scope, got %d", len(out))
	}
	// A nil db would panic if the query were attempted; reaching here
	// proves it was not.
}
