package git

import (
	"reflect"
	"testing"
)

func TestParseChunkHeader(t *testing.T) {
	tests := []struct {
		header   string
		oldStart int
		oldLen   int
		newStart int
		newLen   int
		wantErr  bool
	}{
		{"@@ -10,3 +12,4 @@", 10, 3, 12, 4, false},
		{"@@ -10 +12,4 @@", 10, 1, 12, 4, false},
		{"@@ -10,3 +12 @@", 10, 3, 12, 1, false},
		{"@@ -10 +12 @@", 10, 1, 12, 1, false},
		{"@@ -10,0 +12,5 @@", 10, 0, 12, 5, false},
		{"@@ -10,5 +12,0 @@", 10, 5, 12, 0, false},
		{"invalid header", 0, 0, 0, 0, true},
		{"@@ -10 @@", 0, 0, 0, 0, true},
	}

	for _, tt := range tests {
		oStart, oLen, nStart, nLen, err := parseChunkHeader(tt.header)
		if (err != nil) != tt.wantErr {
			t.Errorf("parseChunkHeader(%q) error = %v, wantErr = %v", tt.header, err, tt.wantErr)
			continue
		}
		if !tt.wantErr {
			if oStart != tt.oldStart || oLen != tt.oldLen || nStart != tt.newStart || nLen != tt.newLen {
				t.Errorf("parseChunkHeader(%q) got (%d, %d, %d, %d), want (%d, %d, %d, %d)",
					tt.header, oStart, oLen, nStart, nLen, tt.oldStart, tt.oldLen, tt.newStart, tt.newLen)
			}
		}
	}
}

func TestParseDiff(t *testing.T) {
	diffOutput := `diff --git a/added.go b/added.go
new file mode 100644
index 0000000..1234567
--- /dev/null
+++ b/added.go
@@ -0,0 +1,5 @@
+package main
+
+func main() {
+	println("hello")
+}
diff --git a/deleted.go b/deleted.go
deleted file mode 100644
index 1234567..0000000
--- a/deleted.go
+++ /dev/null
@@ -1,3 +0,0 @@
-package main
-
-func deleted() {}
diff --git a/modified.go b/modified.go
index 1234567..7890123 100644
--- a/modified.go
+++ b/modified.go
@@ -10,3 +10,5 @@
-func old() {
-	// content
-}
+func new() {
+	// content
+	// added line
+}
@@ -30 +32,0 @@
-func anotherOld() {}
`

	expected := []FileDiff{
		{
			Path:   "added.go",
			Status: StatusAdded,
			OldRanges: nil,
			NewRanges: []LineRange{{Start: 1, End: 5}},
		},
		{
			Path:   "deleted.go",
			Status: StatusDeleted,
			OldRanges: []LineRange{{Start: 1, End: 3}},
			NewRanges: nil,
		},
		{
			Path:   "modified.go",
			Status: StatusModified,
			OldRanges: []LineRange{
				{Start: 10, End: 12},
				{Start: 30, End: 30},
			},
			NewRanges: []LineRange{
				{Start: 10, End: 14},
			},
		},
	}

	got, err := ParseDiff(diffOutput)
	if err != nil {
		t.Fatalf("ParseDiff failed: %v", err)
	}

	if !reflect.DeepEqual(got, expected) {
		t.Errorf("ParseDiff output mismatch.\nGot: %+v\nWant: %+v", got, expected)
	}
}
