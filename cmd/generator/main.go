// Copyright (c) 2024 Alexey Mayshev and contributors. All rights reserved.
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

package main

import (
	"bytes"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

type feature struct {
	name string
}

func newFeature(name string) feature {
	return feature{
		name: name,
	}
}

func (f feature) alias() string {
	return string(f.name[0])
}

var (
	size       = newFeature("size")
	expiration = newFeature("expiration")
	refresh    = newFeature("refresh")
	weight     = newFeature("weight")

	declaredFeatures = []feature{
		size,
		expiration,
		refresh,
		weight,
	}

	nodeTypes      []string
	aliasToFeature map[string]feature
)

// Value storages of nodes with state (the only ones that are updated in place). The storage is
// chosen from the value type once, when the cache is created (see valueStorage in the manager).
const (
	// inlineStorage keeps the value inline until the node is replaced by a boxed one, whose value
	// lives behind an atomic pointer.
	inlineStorage = ""
	// pointerStorage keeps a pointer-shaped value (*T, map, chan, func) in an atomic pointer.
	pointerStorage = "p"
	// wordStorage keeps a value without pointers of up to 8 bytes in an atomic.Uint64. A smaller
	// value takes the same word: a separate 32-bit storage would rarely make the node smaller
	// (only next to a key of at most 4 bytes), but would add node types, each of which every
	// program instantiates for every cache type it uses.
	wordStorage = "u64"
)

var storages = []string{inlineStorage, pointerStorage, wordStorage}

// stateFeatures are the features that give a node state, i.e. the cache maintenance: only such
// nodes are updated in place and get the other value storages.
var stateFeatures = []feature{size, expiration, weight}

func hasState(features map[feature]bool) bool {
	for _, f := range stateFeatures {
		if features[f] {
			return true
		}
	}
	return false
}

// storagesOf returns the value storages generated for nodeType.
func storagesOf(nodeType string) []string {
	if !hasState(getFeatures(nodeType)) {
		// no state: the cache has no maintenance and never updates a node in place
		return []string{inlineStorage}
	}
	return storages
}

func structNameOf(nodeType, storage string) string {
	return strings.ToUpper(nodeType + storage)
}

func init() {
	aliasToFeature = make(map[string]feature, len(declaredFeatures))
	for _, f := range declaredFeatures {
		aliasToFeature[f.alias()] = f
	}

	enabled := make([][]bool, len(declaredFeatures))
	for i := 0; i < len(enabled); i++ {
		enabled[i] = []bool{false, true}
	}

	// cartesian product
	total := len(enabled)
	totalCombinations := 1 << total
	combinations := make([][]bool, 0, totalCombinations)
	for i := 0; i < totalCombinations; i++ {
		combination := make([]bool, 0, total)
		for j := 0; j < total; j++ {
			if ((i >> j) & 1) == 1 {
				combination = append(combination, enabled[j][0])
			} else {
				combination = append(combination, enabled[j][1])
			}
		}
		combinations = append(combinations, combination)
	}

	featureToIdx := make(map[feature]int, len(declaredFeatures))
	for i, f := range declaredFeatures {
		featureToIdx[f] = i
	}

	nodeTypesSet := make(map[string]bool, len(combinations))
	for _, combination := range combinations {
		featureSet := make(map[feature]bool)
		for i := 0; i < len(combination); i++ {
			if combination[i] {
				featureSet[declaredFeatures[i]] = true
			}
		}
		if featureSet[size] {
			delete(featureSet, weight)
		}
		features := make([]feature, 0, len(featureSet))
		for f := range featureSet {
			features = append(features, f)
		}
		sort.Slice(features, func(i, j int) bool {
			return featureToIdx[features[i]] < featureToIdx[features[j]]
		})

		var sb strings.Builder
		sb.WriteString("b")
		for _, f := range features {
			sb.WriteString(f.alias())
		}
		nodeTypesSet[sb.String()] = true
	}

	nodeTypes = make([]string, 0, len(nodeTypesSet))
	for nodeType := range nodeTypesSet {
		nodeTypes = append(nodeTypes, nodeType)
	}
	sort.Slice(nodeTypes, func(i, j int) bool {
		return nodeTypes[i] < nodeTypes[j]
	})
}

func getFeatures(nodeType string) map[feature]bool {
	features := make(map[feature]bool, len(nodeType)-1)
	for _, alias := range nodeType[1:] {
		feature, ok := aliasToFeature[string(alias)]
		if !ok {
			panic("not valid node alias")
		}

		features[feature] = true
	}
	return features
}

type writer struct {
	buf    bytes.Buffer
	indent string
}

func newWriter() *writer {
	return &writer{}
}

func (w *writer) p(format string, args ...any) {
	fmt.Fprintf(&w.buf, w.indent+format+"\n", args...)
}

func (w *writer) in() {
	w.indent += "\t"
}

func (w *writer) out() {
	if w.indent != "" {
		w.indent = w.indent[0 : len(w.indent)-1]
	}
}

func (w *writer) output() []byte {
	return w.buf.Bytes()
}

type generator struct {
	*writer

	structName string
	storage    string
	features   map[feature]bool
}

func newGenerator(nodeType, storage string) *generator {
	return &generator{
		writer:     newWriter(),
		structName: structNameOf(nodeType, storage),
		storage:    storage,
		features:   getFeatures(nodeType),
	}
}

// inWord reports whether the value is kept in one atomic word and can be replaced in place
// from the start.
func (g *generator) inWord() bool {
	return g.storage != inlineStorage
}

func (g *generator) isBounded() bool {
	return g.features[size] || g.features[weight]
}

func (g *generator) withState() bool {
	return hasState(g.features)
}

func (g *generator) printImports() {
	g.p("import (")
	g.in()
	if g.withState() || g.features[refresh] {
		g.p("\"sync/atomic\"")
	}
	g.p("\"unsafe\"")
	g.out()
	g.p(")")
	g.p("")
}

func (g *generator) printStructComment() {
	g.p("// %s is a cache entry that provide the following features:", g.structName)
	g.p("//")
	g.p("// 1. Base")
	i := 2
	for _, f := range declaredFeatures {
		if g.features[f] {
			//nolint:staticcheck // used only for unicode
			featureTitle := strings.Title(strings.ToLower(f.name))
			g.p("//")
			g.p("// %d. %s", i, featureTitle)
			i++
		}
	}
	switch g.storage {
	case pointerStorage:
		g.p("//")
		g.p("// The value is pointer-shaped and kept in an atomic pointer.")
	case wordStorage:
		g.p("//")
		g.p("// The value has no pointers, takes at most 8 bytes and is kept in an atomic.Uint64.")
	}
}

func (g *generator) printStruct() {
	g.printStructComment()

	// print struct definition
	g.p("type %s[K comparable, V any] struct {", g.structName)
	g.in()
	g.p("key        K")
	switch g.storage {
	case pointerStorage:
		// accessed only atomically; holds the value's pointer word, so the GC sees it
		g.p("value      unsafe.Pointer")
	case wordStorage:
		g.p("value      atomic.Uint64")
	default:
		g.p("value      V")
	}
	if g.withState() && !g.inWord() {
		// value is immutable once the node is published. A node that has been updated is
		// replaced (RCU) by a boxed node, whose current value lives behind valuePtr and can be
		// swapped in place; valuePtr never goes back to nil. Readers therefore never observe a
		// write to value. Nodes without state belong to caches without maintenance, which never
		// update in place, so they do not pay for the pointer.
		g.p("valuePtr   atomic.Pointer[V]")
	}

	if g.isBounded() {
		g.p("prev       *%s[K, V]", g.structName)
		g.p("next       *%s[K, V]", g.structName)
	}
	if g.features[expiration] {
		g.p("prevExp    *%s[K, V]", g.structName)
		g.p("nextExp    *%s[K, V]", g.structName)
		g.p("expiresAt  atomic.Int64")
	}
	if g.features[refresh] {
		g.p("refreshableAt atomic.Int64")
	}
	if g.features[weight] {
		// weight is the writer's view, updated in place under the hash table's lock and read
		// by lock-free readers; policyWeight is the weight the eviction policy has accounted
		// for, accessed only under the eviction lock.
		g.p("weight     atomic.Uint32")
		g.p("policyWeight uint32")
	}

	if g.withState() {
		g.p("state      atomic.Uint32")
	}
	if g.isBounded() {
		g.p("queueType  uint8")
	}
	g.out()
	g.p("}")
	g.p("")
}

func (g *generator) printConstructors() {
	g.p("// New%s creates a new %s.", g.structName, g.structName)
	g.p("func New%s[K comparable, V any](key K, value V, expiresAt, refreshableAt int64, weight uint32) Node[K, V] {", g.structName)
	g.in()
	g.p("n := &%s[K, V]{", g.structName)
	g.in()
	g.p("key:        key,")
	if !g.inWord() {
		g.p("value:      value,")
	}
	if g.features[weight] {
		g.p("policyWeight: weight,")
	}
	g.out()
	g.p("}")
	if g.inWord() {
		g.p("n.SetValue(value)")
	}
	if g.features[weight] {
		g.p("n.weight.Store(weight)")
	}
	if g.features[expiration] {
		g.p("n.expiresAt.Store(expiresAt)")
	}
	if g.features[refresh] {
		g.p("n.refreshableAt.Store(refreshableAt)")
	}
	if g.withState() {
		g.p("n.state.Store(aliveState)")
	}
	g.p("")
	g.p("return n")
	g.out()
	g.p("}")
	g.p("")

	g.p("// CastPointerTo%s casts a pointer to %s.", g.structName, g.structName)
	g.p("func CastPointerTo%s[K comparable, V any](ptr unsafe.Pointer) Node[K, V] {", g.structName)
	g.in()
	g.p("return (*%s[K, V])(ptr)", g.structName)
	g.out()
	g.p("}")
	g.p("")
}

func (g *generator) printFunctions() {
	g.p("func (n *%s[K, V]) Key() K {", g.structName)
	g.in()
	g.p("return n.key")
	g.out()
	g.p("}")
	g.p("")

	g.p("func (n *%s[K, V]) Value() V {", g.structName)
	g.in()
	switch {
	case g.storage == pointerStorage:
		g.p("p := atomic.LoadPointer(&n.value)")
		g.p("return *(*V)(unsafe.Pointer(&p))")
	case g.inWord():
		g.p("w := n.value.Load()")
		g.p("return *(*V)(unsafe.Pointer(&w))")
	case g.withState():
		g.p("if p := n.valuePtr.Load(); p != nil {")
		g.in()
		g.p("return *p")
		g.out()
		g.p("}")
		g.p("return n.value")
	default:
		g.p("return n.value")
	}
	g.out()
	g.p("}")
	g.p("")

	g.p("func (n *%s[K, V]) SetValue(v V) {", g.structName)
	g.in()
	switch {
	case g.storage == pointerStorage:
		g.p("atomic.StorePointer(&n.value, *(*unsafe.Pointer)(unsafe.Pointer(&v)))")
	case g.storage == wordStorage:
		g.p("var w uint64")
		g.p("*(*V)(unsafe.Pointer(&w)) = v")
		g.p("n.value.Store(w)")
	case g.withState():
		g.p("n.valuePtr.Store(&v)")
	default:
		g.p("panic(\"not implemented\")")
	}
	g.out()
	g.p("}")
	g.p("")

	g.p("func (n *%s[K, V]) CanSetValue() bool {", g.structName)
	g.in()
	switch {
	case g.inWord():
		g.p("return true")
	case g.withState():
		g.p("return n.valuePtr.Load() != nil")
	default:
		g.p("return false")
	}
	g.out()
	g.p("}")
	g.p("")

	g.p("func (n *%s[K, V]) AsPointer() unsafe.Pointer {", g.structName)
	g.in()
	g.p("return unsafe.Pointer(n)")
	g.out()
	g.p("}")
	g.p("")

	g.p("func (n *%s[K, V]) Prev() Node[K, V] {", g.structName)
	g.in()
	if g.isBounded() {
		g.p("return n.prev")
	} else {
		g.p("panic(\"not implemented\")")
	}
	g.out()
	g.p("}")
	g.p("")

	g.p("func (n *%s[K, V]) SetPrev(v Node[K, V]) {", g.structName)
	g.in()
	if g.isBounded() {
		g.p("if v == nil {")
		g.in()
		g.p("n.prev = nil")
		g.p("return")
		g.out()
		g.p("}")
		g.p("n.prev = (*%s[K, V])(v.AsPointer())", g.structName)
	} else {
		g.p("panic(\"not implemented\")")
	}
	g.out()
	g.p("}")
	g.p("")

	g.p("func (n *%s[K, V]) Next() Node[K, V] {", g.structName)
	g.in()
	if g.isBounded() {
		g.p("return n.next")
	} else {
		g.p("panic(\"not implemented\")")
	}
	g.out()
	g.p("}")
	g.p("")

	g.p("func (n *%s[K, V]) SetNext(v Node[K, V]) {", g.structName)
	g.in()
	if g.isBounded() {
		g.p("if v == nil {")
		g.in()
		g.p("n.next = nil")
		g.p("return")
		g.out()
		g.p("}")
		g.p("n.next = (*%s[K, V])(v.AsPointer())", g.structName)
	} else {
		g.p("panic(\"not implemented\")")
	}
	g.out()
	g.p("}")
	g.p("")

	g.p("func (n *%s[K, V]) PrevExp() Node[K, V] {", g.structName)
	g.in()
	if g.features[expiration] {
		g.p("return n.prevExp")
	} else {
		g.p("panic(\"not implemented\")")
	}
	g.out()
	g.p("}")
	g.p("")

	g.p("func (n *%s[K, V]) SetPrevExp(v Node[K, V]) {", g.structName)
	g.in()
	if g.features[expiration] {
		g.p("if v == nil {")
		g.in()
		g.p("n.prevExp = nil")
		g.p("return")
		g.out()
		g.p("}")
		g.p("n.prevExp = (*%s[K, V])(v.AsPointer())", g.structName)
	} else {
		g.p("panic(\"not implemented\")")
	}
	g.out()
	g.p("}")
	g.p("")

	g.p("func (n *%s[K, V]) NextExp() Node[K, V] {", g.structName)
	g.in()
	if g.features[expiration] {
		g.p("return n.nextExp")
	} else {
		g.p("panic(\"not implemented\")")
	}
	g.out()
	g.p("}")
	g.p("")

	g.p("func (n *%s[K, V]) SetNextExp(v Node[K, V]) {", g.structName)
	g.in()
	if g.features[expiration] {
		g.p("if v == nil {")
		g.in()
		g.p("n.nextExp = nil")
		g.p("return")
		g.out()
		g.p("}")
		g.p("n.nextExp = (*%s[K, V])(v.AsPointer())", g.structName)
	} else {
		g.p("panic(\"not implemented\")")
	}
	g.out()
	g.p("}")
	g.p("")

	g.p("func (n *%s[K, V]) HasExpired(now int64) bool {", g.structName)
	g.in()
	if g.features[expiration] {
		g.p("return n.ExpiresAt() <= now")
	} else {
		g.p("return false")
	}
	g.out()
	g.p("}")
	g.p("")

	g.p("func (n *%s[K, V]) ExpiresAt() int64 {", g.structName)
	g.in()
	if g.features[expiration] {
		g.p("return n.expiresAt.Load()")
	} else {
		g.p("panic(\"not implemented\")")
	}
	g.out()
	g.p("}")
	g.p("")

	g.p("func (n *%s[K, V]) CASExpiresAt(old, new int64) bool {", g.structName)
	g.in()
	if g.features[expiration] {
		g.p("return n.expiresAt.CompareAndSwap(old, new)")
	} else {
		g.p("panic(\"not implemented\")")
	}
	g.out()
	g.p("}")
	g.p("")

	g.p("func (n *%s[K, V]) SetExpiresAt(new int64) {", g.structName)
	g.in()
	if g.features[expiration] {
		g.p("n.expiresAt.Store(new)")
	} else {
		g.p("panic(\"not implemented\")")
	}
	g.out()
	g.p("}")
	g.p("")

	g.p("func (n *%s[K, V]) RefreshableAt() int64 {", g.structName)
	g.in()
	if g.features[refresh] {
		g.p("return n.refreshableAt.Load()")
	} else {
		g.p("panic(\"not implemented\")")
	}
	g.out()
	g.p("}")
	g.p("")

	g.p("func (n *%s[K, V]) CASRefreshableAt(old, new int64) bool {", g.structName)
	g.in()
	if g.features[refresh] {
		g.p("return n.refreshableAt.CompareAndSwap(old, new)")
	} else {
		g.p("panic(\"not implemented\")")
	}
	g.out()
	g.p("}")
	g.p("")

	g.p("func (n *%s[K, V]) SetRefreshableAt(new int64) {", g.structName)
	g.in()
	if g.features[refresh] {
		g.p("n.refreshableAt.Store(new)")
	} else {
		g.p("panic(\"not implemented\")")
	}
	g.out()
	g.p("}")
	g.p("")

	g.p("func (n *%s[K, V]) IsFresh(now int64) bool {", g.structName)
	g.in()
	if g.features[refresh] {
		g.p("return n.IsAlive() && n.RefreshableAt() > now")
	} else {
		g.p("return true")
	}
	g.out()
	g.p("}")
	g.p("")

	g.p("func (n *%s[K, V]) Weight() uint32 {", g.structName)
	g.in()
	if g.features[weight] {
		g.p("return n.weight.Load()")
	} else {
		g.p("return 1")
	}
	g.out()
	g.p("}")
	g.p("")

	g.p("func (n *%s[K, V]) SetWeight(weight uint32) {", g.structName)
	g.in()
	if g.features[weight] {
		g.p("n.weight.Store(weight)")
	} else {
		g.p("panic(\"not implemented\")")
	}
	g.out()
	g.p("}")
	g.p("")

	g.p("func (n *%s[K, V]) PolicyWeight() uint32 {", g.structName)
	g.in()
	if g.features[weight] {
		g.p("return n.policyWeight")
	} else {
		g.p("return 1")
	}
	g.out()
	g.p("}")
	g.p("")

	g.p("func (n *%s[K, V]) SetPolicyWeight(weight uint32) {", g.structName)
	g.in()
	if g.features[weight] {
		g.p("n.policyWeight = weight")
	}
	g.out()
	g.p("}")
	g.p("")

	g.p("func (n *%s[K, V]) IsAlive() bool {", g.structName)
	g.in()
	if g.withState() {
		g.p("return n.state.Load() == aliveState")
	} else {
		g.p("return true")
	}
	g.out()
	g.p("}")
	g.p("")

	g.p("func (n *%s[K, V]) IsRetired() bool {", g.structName)
	g.in()
	if g.withState() {
		g.p("return n.state.Load() == retiredState")
	} else {
		g.p("panic(\"not implemented\")")
	}
	g.out()
	g.p("}")
	g.p("")

	g.p("func (n *%s[K, V]) Retire() {", g.structName)
	g.in()
	if g.withState() {
		g.p("n.state.Store(retiredState)")
	} else {
		g.p("panic(\"not implemented\")")
	}
	g.out()
	g.p("}")
	g.p("")

	g.p("func (n *%s[K, V]) IsDead() bool {", g.structName)
	g.in()
	if g.withState() {
		g.p("return n.state.Load() == deadState")
	} else {
		g.p("panic(\"not implemented\")")
	}
	g.out()
	g.p("}")
	g.p("")

	g.p("func (n *%s[K, V]) Die() {", g.structName)
	g.in()
	if g.withState() {
		g.p("n.state.Store(deadState)")
	} else {
		g.p("panic(\"not implemented\")")
	}
	g.out()
	g.p("}")
	g.p("")

	g.p("func (n *%s[K, V]) GetQueueType() uint8 {", g.structName)
	g.in()
	if g.isBounded() {
		g.p("return n.queueType")
	} else {
		g.p("panic(\"not implemented\")")
	}
	g.out()
	g.p("}")
	g.p("")

	g.p("func (n *%s[K, V]) SetQueueType(queueType uint8) {", g.structName)
	g.in()
	if g.isBounded() {
		g.p("n.queueType = queueType")
	} else {
		g.p("panic(\"not implemented\")")
	}
	g.out()
	g.p("}")
	g.p("")

	g.p("func (n *%s[K, V]) InWindow() bool {", g.structName)
	g.in()
	g.p("return n.GetQueueType() == InWindowQueue")
	g.out()
	g.p("}")
	g.p("")

	g.p("func (n *%s[K, V]) MakeWindow() {", g.structName)
	g.in()
	g.p("n.SetQueueType(InWindowQueue)")
	g.out()
	g.p("}")
	g.p("")

	g.p("func (n *%s[K, V]) InMainProbation() bool {", g.structName)
	g.in()
	g.p("return n.GetQueueType() == InMainProbationQueue")
	g.out()
	g.p("}")
	g.p("")

	g.p("func (n *%s[K, V]) MakeMainProbation() {", g.structName)
	g.in()
	g.p("n.SetQueueType(InMainProbationQueue)")
	g.out()
	g.p("}")
	g.p("")

	g.p("func (n *%s[K, V]) InMainProtected() bool {", g.structName)
	g.in()
	g.p("return n.GetQueueType() == InMainProtectedQueue")
	g.out()
	g.p("}")
	g.p("")

	g.p("func (n *%s[K, V]) MakeMainProtected() {", g.structName)
	g.in()
	g.p("n.SetQueueType(InMainProtectedQueue)")
	g.out()
	g.p("}")
	g.p("")
}

func run(nodeType, storage, dir string) error {
	g := newGenerator(nodeType, storage)
	g.p("// Code generated by NodeGenerator. DO NOT EDIT.")
	g.p("")
	g.p("// Package node is a generated by the generator.")
	g.p("package node")
	g.p("")

	g.printImports()

	g.printStruct()
	g.printConstructors()

	g.printFunctions()

	fileName := fmt.Sprintf("%s.go", nodeType+storage)
	filePath := filepath.Join(dir, fileName)

	f, err := os.Create(filePath)
	if err != nil {
		return fmt.Errorf("create file %s: %w", filePath, err)
	}
	defer f.Close()

	if _, err := f.Write(g.output()); err != nil {
		return fmt.Errorf("write output: %w", err)
	}

	return nil
}

func main() {
	dir := os.Args[1]

	if err := os.RemoveAll(dir); err != nil {
		log.Fatalf("remove dir: %s\n", err.Error())
	}

	if err := os.MkdirAll(dir, os.ModePerm); err != nil {
		log.Fatalf("create dir %s: %s", dir, err.Error())
	}

	for _, nodeType := range nodeTypes {
		for _, storage := range storagesOf(nodeType) {
			if err := run(nodeType, storage, dir); err != nil {
				log.Fatal(err)
			}
		}
	}

	if err := printManager(dir); err != nil {
		log.Fatal(err)
	}
}
