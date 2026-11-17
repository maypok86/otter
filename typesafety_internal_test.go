// Copyright (c) 2026 Alexey Mayshev and contributors. All rights reserved.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package otter

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/maypok86/otter/v2/internal/generated/node"
)

const allowedUnsafePkg = "github.com/maypok86/otter/v2/internal/generated/node"

type goListPackage struct {
	ImportPath   string   `json:"ImportPath"`
	Imports      []string `json:"Imports"`
	TestImports  []string `json:"TestImports"`
	XTestImports []string `json:"XTestImports"`
}

func goBinaryPath() string {
	if _, err := os.Stat("/usr/bin/go"); err == nil {
		return "/usr/bin/go"
	}
	return "go"
}

func TestUnsafeImportBoundary(t *testing.T) {
	t.Parallel()

	modRoot, err := os.Getwd()
	require.NoError(t, err)

	cmd := exec.Command(goBinaryPath(), "list", "-json", "./...")
	cmd.Dir = modRoot
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, "go list failed:\n%s", string(out))

	dec := json.NewDecoder(bytes.NewReader(out))
	var sawNodePkg bool
	for {
		var pkg goListPackage
		if err := dec.Decode(&pkg); err != nil {
			require.ErrorIs(t, err, io.EOF)
			break
		}
		if pkg.ImportPath == allowedUnsafePkg {
			sawNodePkg = true
			require.True(t, slices.Contains(pkg.Imports, "unsafe"),
				"%s is expected to encapsulate unsafe", allowedUnsafePkg)
			continue
		}
		require.False(t, slices.Contains(pkg.Imports, "unsafe"),
			"package %s must NOT import unsafe in Imports: %v", pkg.ImportPath, pkg.Imports)
		require.False(t, slices.Contains(pkg.TestImports, "unsafe"),
			"package %s must NOT import unsafe in TestImports: %v", pkg.ImportPath, pkg.TestImports)
		require.False(t, slices.Contains(pkg.XTestImports, "unsafe"),
			"package %s must NOT import unsafe in XTestImports: %v", pkg.ImportPath, pkg.XTestImports)
	}
	require.True(t, sawNodePkg, "did not observe %s in go list ./...", allowedUnsafePkg)

	// Also verify PointerOf and AsPointer are completely absent from generated node sources.
	nodeDir := filepath.Join(modRoot, "internal", "generated", "node")
	entries, err := os.ReadDir(nodeDir)
	require.NoError(t, err)
	for _, entry := range entries {
		if !strings.HasSuffix(entry.Name(), ".go") || strings.HasSuffix(entry.Name(), "_test.go") {
			continue
		}
		src, err := os.ReadFile(filepath.Join(nodeDir, entry.Name()))
		require.NoError(t, err)
		text := string(src)
		require.NotContains(t, text, "PointerOf", "%s still contains PointerOf", entry.Name())
		require.NotContains(t, text, "AsPointer", "%s still contains AsPointer", entry.Name())
	}
}

func TestHandleZeroOverheadAndComparability(t *testing.T) {
	t.Parallel()

	// Entry[K, V] must remain Size == 1, Align == 1, PtrBytes == 0, and Comparable == true
	// even when V itself is non-comparable ([]byte, func(), map[string]int).
	handleTypes := []reflect.Type{
		reflect.TypeFor[node.Entry[int, int]](),
		reflect.TypeFor[node.Entry[string, []byte]](),
		reflect.TypeFor[node.Entry[string, func()]](),
		reflect.TypeFor[node.Entry[string, map[string]int]](),
		reflect.TypeFor[node.Keyed[int]](),
		reflect.TypeFor[node.Keyed[string]](),
		reflect.TypeFor[node.Header](),
		reflect.TypeFor[node.Sentinel](),
	}
	for _, ht := range handleTypes {
		require.Equal(t, uintptr(1), ht.Size(), "%s Size()", ht)
		require.Equal(t, 1, ht.Align(), "%s Align()", ht)
		require.True(t, ht.Comparable(), "%s must be comparable", ht)
		// Verify zero GC pointer bytes via reflect: no pointer-bearing non-zero-length fields.
		for i := range ht.NumField() {
			f := ht.Field(i)
			if f.Type.Size() > 0 {
				require.Equal(t, reflect.Uint8, f.Type.Kind(), "%s non-zero field %s", ht, f.Name)
			}
		}
	}

	// Pointer handles must be 1 word (same size as uintptr) and comparable.
	ptrSize := reflect.TypeFor[uintptr]().Size()
	require.Equal(t, ptrSize, reflect.TypeFor[*node.Entry[string, []byte]]().Size())
	require.Equal(t, ptrSize, reflect.TypeFor[*node.Keyed[string]]().Size())
	require.Equal(t, ptrSize, reflect.TypeFor[*node.Header]().Size())
	require.Equal(t, ptrSize, reflect.TypeFor[*node.Sentinel]().Size())

	// KeyLens[K] must be sealed: at least 1 field, and every field must be unexported.
	lensType := reflect.TypeFor[node.KeyLens[string]]()
	require.GreaterOrEqual(t, lensType.NumField(), 1)
	for i := range lensType.NumField() {
		f := lensType.Field(i)
		require.False(t, f.IsExported(), "KeyLens field %q must be unexported so offsets cannot be forged", f.Name)
	}
}

func TestCompileTimeNegativeRejections(t *testing.T) {
	t.Parallel()

	modRoot, err := os.Getwd()
	require.NoError(t, err)

	runOverlayBuild := func(t *testing.T, virtualRelPath, code string) (int, string) {
		t.Helper()
		tmpDir := t.TempDir()
		physFile := filepath.Join(tmpDir, "snippet.go")
		require.NoError(t, os.WriteFile(physFile, []byte(code), 0o600))

		virtFile := filepath.Join(modRoot, filepath.FromSlash(virtualRelPath))
		overlayData, err := json.Marshal(struct {
			Replace map[string]string `json:"Replace"`
		}{
			Replace: map[string]string{virtFile: physFile},
		})
		require.NoError(t, err)

		overlayPath := filepath.Join(tmpDir, "overlay.json")
		require.NoError(t, os.WriteFile(overlayPath, overlayData, 0o600))

		pkgArg := "."
		if dir := filepath.Dir(virtualRelPath); dir != "." {
			pkgArg = "./" + filepath.ToSlash(dir)
		}
		outBin := filepath.Join(tmpDir, "out.a")
		cmd := exec.Command(goBinaryPath(), "build", "-overlay="+overlayPath, "-o", outBin, pkgArg)
		cmd.Dir = modRoot
		out, buildErr := cmd.CombinedOutput()
		if buildErr == nil {
			return 0, string(out)
		}
		var exitErr *exec.ExitError
		require.ErrorAs(t, buildErr, &exitErr)
		return exitErr.ExitCode(), string(out)
	}

	t.Run("pos01_positive_control", func(t *testing.T) {
		t.Parallel()
		const src = `package otter

import (
	"github.com/maypok86/otter/v2/internal/expiration"
	"github.com/maypok86/otter/v2/internal/generated/node"
	"github.com/maypok86/otter/v2/internal/hashmap"
)

func _typecheckPositiveControl() {
	nm := node.NewManager[string, int](node.Config{WithSize: true, WithExpiration: true})
	e := nm.Create("hello", 42, 100, 0, 1)
	_ = nm.Key(e)
	_ = nm.Value(e)

	lens := nm.KeyLens()
	k := e.Keyed()
	_ = lens.Key(k)

	em := hashmap.NewEntryMap(nm)
	em.Compute("hello", func(*node.Entry[string, int]) *node.Entry[string, int] { return e })

	p := newPolicy[string](false, 16, nm.Kind, lens, func(*node.Keyed[string]) bool { return true })
	p.add(k)

	w := expiration.NewTypedWheel(nm)
	w.Schedule(e)
	_ = nm.NewSentinel()
}
`
		code, out := runOverlayBuild(t, "negcheck_pos01_probe.go", src)
		require.Equal(t, 0, code, "positive control must compile cleanly:\n%s", out)
	})

	t.Run("neg01_cross_cache_kv_mixing", func(t *testing.T) {
		t.Parallel()
		const src = `package otter

import "github.com/maypok86/otter/v2/internal/generated/node"

func _neg01CrossCacheMix() {
	nmA := node.NewManager[int, string](node.Config{WithSize: true})
	nmB := node.NewManager[string, int](node.Config{WithSize: true})
	entryA := nmA.Create(1, "one", 0, 0, 1)
	_ = nmB.Value(entryA)
}
`
		code, out := runOverlayBuild(t, "negcheck_neg01_probe.go", src)
		require.NotEqual(t, 0, code, "neg01 cross-cache (K,V) mixing must fail compilation")
		require.Contains(t, out, "cannot use entryA")
	})

	t.Run("neg02_cross_key_policy_and_hashmap_mixing", func(t *testing.T) {
		t.Parallel()
		const src = `package otter

import (
	"github.com/maypok86/otter/v2/internal/generated/node"
	"github.com/maypok86/otter/v2/internal/hashmap"
)

func _neg02CrossKeyMix() {
	nmInt := node.NewManager[int, int](node.Config{WithSize: true})
	nmStr := node.NewManager[string, int](node.Config{WithSize: true})
	kInt := nmInt.Create(1, 10, 0, 0, 1).Keyed()

	pStr := newPolicy[string](false, 16, nmStr.Kind, nmStr.KeyLens(), func(*node.Keyed[string]) bool { return true })
	pStr.access(kInt)

	mStr := hashmap.New[string, node.Keyed[string]](nmStr.KeyLens().Key)
	mStr.Compute("x", func(*node.Keyed[string]) *node.Keyed[string] { return kInt })
}
`
		code, out := runOverlayBuild(t, "negcheck_neg02_probe.go", src)
		require.NotEqual(t, 0, code, "neg02 cross-key policy/hashmap mixing must fail compilation")
		require.Contains(t, out, "cannot use kInt")
	})

	t.Run("neg03_sentinel_rejected_by_key_value_policy_and_wheel", func(t *testing.T) {
		t.Parallel()
		const src = `package otter

import (
	"github.com/maypok86/otter/v2/internal/expiration"
	"github.com/maypok86/otter/v2/internal/generated/node"
)

func _neg03SentinelMix() {
	nm := node.NewManager[int, int](node.Config{WithSize: true, WithExpiration: true})
	s := nm.NewSentinel()
	_ = nm.Key(s)
	_ = nm.Value(s)
	_ = nm.KeyLens().Key(s)

	p := newPolicy[int](false, 16, nm.Kind, nm.KeyLens(), func(*node.Keyed[int]) bool { return true })
	p.access(s)

	w := expiration.NewTypedWheel(nm)
	w.Schedule(s)
}
`
		code, out := runOverlayBuild(t, "negcheck_neg03_probe.go", src)
		require.NotEqual(t, 0, code, "neg03 passing *Sentinel to Key/Value/policy/TypedWheel must fail compilation")
		require.Contains(t, out, "cannot use s")
	})

	t.Run("neg04_unforgeable_keylens", func(t *testing.T) {
		t.Parallel()
		const src = `package negcheck

import "github.com/maypok86/otter/v2/internal/generated/node"

func ForgedLens() node.KeyLens[string] {
	return node.KeyLens[string]{keyOffset: 9999}
}
`
		code, out := runOverlayBuild(t, "internal/negcheck/forged_lens.go", src)
		require.NotEqual(t, 0, code, "neg04 forging KeyLens with arbitrary uintptr offset must fail compilation")
		require.True(t,
			strings.Contains(out, "unknown field") || strings.Contains(out, "unexported field"),
			"unexpected compiler error for forged KeyLens:\n%s", out,
		)
	})
}

func TestValueUpdateZeroAllocs(t *testing.T) {
	// Not parallel: testing.AllocsPerRun cannot run alongside parallel tests.
	t.Run("u64_in_place_update_zero_allocs", func(t *testing.T) {
		c := Must(&Options[int, uint64]{
			MaximumSize: 64,
			Executor:    func(fn func()) {},
		})
		c.Set(1, 100)
		var step uint64
		allocs := testing.AllocsPerRun(200, func() {
			step++
			c.Set(1, step)
		})
		require.Zero(t, allocs, "live in-place u64 Set update must allocate 0 times")
	})

	t.Run("pointer_in_place_update_zero_allocs", func(t *testing.T) {
		v1, v2 := 10, 20
		p1, p2 := &v1, &v2
		c := Must(&Options[int, *int]{
			MaximumSize: 64,
			Executor:    func(fn func()) {},
		})
		c.Set(1, p1)
		toggle := false
		allocs := testing.AllocsPerRun(200, func() {
			toggle = !toggle
			if toggle {
				c.Set(1, p2)
			} else {
				c.Set(1, p1)
			}
		})
		require.Zero(t, allocs, "live in-place pointer Set update must allocate 0 times")
	})

	t.Run("inline_create_single_alloc_escape_isolated", func(t *testing.T) {
		nm := node.NewManager[int, [64]byte](node.Config{WithSize: true, WithExpiration: true})
		val := [64]byte{1: 42, 63: 99}
		var sink *node.Entry[int, [64]byte]
		allocs := testing.AllocsPerRun(200, func() {
			sink = nm.Create(1, val, 1000, 0, 1)
		})
		require.NotNil(t, sink)
		require.Equal(t, float64(1), allocs, "inline Create must allocate only the node struct (storeBoxedValue escape isolated)")
	})
}

func TestCache_TinyOneByteAlignmentCheckptr(t *testing.T) {
	t.Parallel()

	// BInline[uint8, uint8] has Size == 2, Align == 1, so Go's tiny allocator packs nodes at
	// non-8-byte-aligned addresses inside 16-byte tiny blocks. With Sizeof(Entry) == 1 and
	// Alignof(Entry) == 1, converting unsafe.Pointer to *Entry[uint8, uint8] is 100% checkptr-safe.
	c := Must(&Options[uint8, uint8]{})
	for i := range 256 {
		k := uint8(i)
		c.Set(k, k^0x5a)
	}
	for i := range 256 {
		k := uint8(i)
		v, ok := c.GetIfPresent(k)
		require.True(t, ok)
		require.Equal(t, k^0x5a, v)
	}

	cEmpty := Must(&Options[struct{}, struct{}]{})
	cEmpty.Set(struct{}{}, struct{}{})
	_, ok := cEmpty.GetIfPresent(struct{}{})
	require.True(t, ok)
}
