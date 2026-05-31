package main

import (
	"archive/zip"
	"os"
	"path/filepath"
	"testing"
)

func TestParseVDFPathValues(t *testing.T) {
	input := `"libraryfolders"
{
	"0"
	{
		"path"		"E:\\APP\\steam"
	}
	"1"
	{
		"path"		"D:\\SteamLibrary"
	}
}`

	got, err := parseVDFPathValues(input)
	if err != nil {
		t.Fatalf("parseVDFPathValues returned error: %v", err)
	}
	want := []string{`E:\APP\steam`, `D:\SteamLibrary`}
	if len(got) != len(want) {
		t.Fatalf("got %d paths, want %d: %#v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("path %d = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestFindLogicalRootPrefix(t *testing.T) {
	zipPath := createTestZip(t, map[string]string{
		"汉化v1.0/BepInEx/config/test.cfg": "config",
		"汉化v1.0/readme.txt":              "readme",
	})

	reader, err := zip.OpenReader(zipPath)
	if err != nil {
		t.Fatalf("zip.OpenReader returned error: %v", err)
	}
	defer reader.Close()

	got, err := findLogicalRootPrefix(reader.File)
	if err != nil {
		t.Fatalf("findLogicalRootPrefix returned error: %v", err)
	}
	if got != "汉化v1.0/" {
		t.Fatalf("prefix = %q, want %q", got, "汉化v1.0/")
	}
}

func TestStripLogicalRootRejectsTraversal(t *testing.T) {
	if _, _, err := stripLogicalRoot("汉化v1.0/../evil.dll", "汉化v1.0/"); err == nil {
		t.Fatal("stripLogicalRoot accepted traversal path")
	}
}

func createTestZip(t *testing.T, files map[string]string) string {
	t.Helper()

	zipPath := filepath.Join(t.TempDir(), "test.zip")
	out, err := os.Create(zipPath)
	if err != nil {
		t.Fatalf("os.Create returned error: %v", err)
	}

	writer := zip.NewWriter(out)
	for name, content := range files {
		file, err := writer.Create(name)
		if err != nil {
			t.Fatalf("writer.Create returned error: %v", err)
		}
		if _, err := file.Write([]byte(content)); err != nil {
			t.Fatalf("file.Write returned error: %v", err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("writer.Close returned error: %v", err)
	}
	if err := out.Close(); err != nil {
		t.Fatalf("out.Close returned error: %v", err)
	}

	return zipPath
}
