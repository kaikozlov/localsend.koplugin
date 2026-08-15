package utils

import (
	"os"
	"path/filepath"
	"sync"
	"testing"
)

func TestUniqueFileAllocator_AllocatesSequentialDuplicateNames(t *testing.T) {
	dir := t.TempDir()
	var allocator UniqueFileAllocator
	want := []string{"same.txt", "same (1).txt", "same (2).txt"}
	for _, name := range want {
		file, path, err := allocator.Create(dir, "same.txt")
		if err != nil {
			t.Fatal(err)
		}
		if err := file.Close(); err != nil {
			t.Fatal(err)
		}
		if got := filepath.Base(path); got != name {
			t.Fatalf("allocated %q; want %q", got, name)
		}
	}
}

func TestUniqueFileAllocator_ConcurrentDuplicatesRemainAtomic(t *testing.T) {
	dir := t.TempDir()
	var allocator UniqueFileAllocator
	paths := make(chan string, 2)
	errs := make(chan error, 2)
	var wg sync.WaitGroup
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			file, path, err := allocator.Create(dir, "same.txt")
			if err != nil {
				errs <- err
				return
			}
			_ = file.Close()
			paths <- filepath.Base(path)
		}()
	}
	wg.Wait()
	close(paths)
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for path := range paths {
		seen[path] = true
	}
	if !seen["same.txt"] || !seen["same (1).txt"] || len(seen) != 2 {
		t.Fatalf("concurrent paths = %v; want same.txt and same (1).txt", seen)
	}
	for path := range seen {
		if _, err := os.Stat(filepath.Join(dir, path)); err != nil {
			t.Fatalf("allocated path %q missing: %v", path, err)
		}
	}
}

func BenchmarkUniqueFileAllocator_1000Duplicates(b *testing.B) {
	for i := 0; i < b.N; i++ {
		dir := b.TempDir()
		var allocator UniqueFileAllocator
		b.StartTimer()
		for j := 0; j < 1000; j++ {
			file, _, err := allocator.Create(dir, "same.txt")
			if err != nil {
				b.Fatal(err)
			}
			if err := file.Close(); err != nil {
				b.Fatal(err)
			}
		}
		b.StopTimer()
	}
}
