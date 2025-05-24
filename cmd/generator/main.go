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
	features   map[feature]bool
}

func newGenerator(nodeType string) *generator {
	return &generator{
		writer:     newWriter(),
		structName: strings.ToUpper(nodeType),
		features:   getFeatures(nodeType),
	}
}

func (g *generator) isBounded() bool {
	return g.features[size] || g.features[weight]
}

func (g *generator) withState() bool {
	return g.isBounded() || g.features[expiration]
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
}

func (g *generator) printStruct() {
	g.printStructComment()

	// print struct definition
	g.p("type %s[K comparable, V any] struct {", g.structName)
	g.in()
	g.p("key        K")
	g.p("value      V")

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
		g.p("weight     uint32")
	}

	if g.withState() {
		g.p("state      atomic.Uint32")
	}
	if g.isBounded() {
		g.p("frequency  uint8")
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
	g.p("value:      value,")
	if g.features[weight] {
		g.p("weight:     weight,")
	}
	g.out()
	g.p("}")
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
	g.p("return n.value")
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
		g.p("return n.weight")
	} else {
		g.p("return 1")
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

	g.p("func (n *%s[K, V]) Frequency() uint8 {", g.structName)
	g.in()
	if g.isBounded() {
		g.p("return n.frequency")
	} else {
		g.p("panic(\"not implemented\")")
	}
	g.out()
	g.p("}")
	g.p("")

	g.p("func (n *%s[K, V]) IncrementFrequency() {", g.structName)
	g.in()
	if g.isBounded() {
		g.p("n.frequency = minUint8(n.frequency+1, maxFrequency)")
	} else {
		g.p("panic(\"not implemented\")")
	}
	g.out()
	g.p("}")
	g.p("")

	g.p("func (n *%s[K, V]) DecrementFrequency() {", g.structName)
	g.in()
	if g.isBounded() {
		g.p("n.frequency--")
	} else {
		g.p("panic(\"not implemented\")")
	}
	g.out()
	g.p("}")
	g.p("")

	g.p("func (n *%s[K, V]) ResetFrequency() {", g.structName)
	g.in()
	if g.isBounded() {
		g.p("n.frequency = 0")
	} else {
		g.p("panic(\"not implemented\")")
	}
	g.out()
	g.p("}")
	g.p("")

	g.p("func (n *%s[K, V]) MarkSmall() {", g.structName)
	g.in()
	if g.isBounded() {
		g.p("n.queueType = smallQueueType")
	} else {
		g.p("panic(\"not implemented\")")
	}
	g.out()
	g.p("}")
	g.p("")

	g.p("func (n *%s[K, V]) IsSmall() bool {", g.structName)
	g.in()
	if g.isBounded() {
		g.p("return n.queueType == smallQueueType")
	} else {
		g.p("panic(\"not implemented\")")
	}
	g.out()
	g.p("}")
	g.p("")

	g.p("func (n *%s[K, V]) MarkMain() {", g.structName)
	g.in()
	if g.isBounded() {
		g.p("n.queueType = mainQueueType")
	} else {
		g.p("panic(\"not implemented\")")
	}
	g.out()
	g.p("}")
	g.p("")

	g.p("func (n *%s[K, V]) IsMain() bool {", g.structName)
	g.in()
	if g.isBounded() {
		g.p("return n.queueType == mainQueueType")
	} else {
		g.p("panic(\"not implemented\")")
	}
	g.out()
	g.p("}")
	g.p("")

	g.p("func (n *%s[K, V]) Unmark() {", g.structName)
	g.in()
	if g.isBounded() {
		g.p("n.queueType = unknownQueueType")
	} else {
		g.p("panic(\"not implemented\")")
	}
	g.out()
	g.p("}")
}

func run(nodeType, dir string) error {
	g := newGenerator(nodeType)
	g.p("// Code generated by NodeGenerator. DO NOT EDIT.")
	g.p("")
	g.p("// Package node is a generated by the generator.")
	g.p("package node")
	g.p("")

	g.printImports()

	g.printStruct()
	g.printConstructors()

	g.printFunctions()

	fileName := fmt.Sprintf("%s.go", nodeType)
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

func printManager(dir string) error {
	const nodeManager = `// Code generated by NodeGenerator. DO NOT EDIT.

// Package node is a generated generator package.
package node

import (
	"strings"
	"unsafe"
)

const (
	unknownQueueType uint8 = iota
	smallQueueType
	mainQueueType

	maxFrequency uint8 = 3
)

const (
	aliveState uint32 = iota
	deadState
)

// Node is a cache entry.
type Node[K comparable, V any] interface {
	// Key returns the key.
	Key() K
	// Value returns the value.
	Value() V
	// AsPointer returns the node as a pointer.
	AsPointer() unsafe.Pointer
	// Prev returns the previous node in the eviction policy.
	Prev() Node[K, V]
	// SetPrev sets the previous node in the eviction policy.
	SetPrev(v Node[K, V])
	// Next returns the next node in the eviction policy.
	Next() Node[K, V]
	// SetNext sets the next node in the eviction policy.
	SetNext(v Node[K, V])
	// PrevExp returns the previous node in the expiration policy.
	PrevExp() Node[K, V]
	// SetPrevExp sets the previous node in the expiration policy.
	SetPrevExp(v Node[K, V])
	// NextExp returns the next node in the expiration policy.
	NextExp() Node[K, V]
	// SetNextExp sets the next node in the expiration policy.
	SetNextExp(v Node[K, V])
	// HasExpired returns true if node has expired.
	HasExpired(now int64) bool
	// ExpiresAt returns the expiration time.
	ExpiresAt() int64
	// CASExpiresAt executes the compare-and-swap operation for expiresAt.
	CASExpiresAt(old, new int64) bool
	// SetExpiresAt sets the expiration time.
	SetExpiresAt(new int64)
	// RefreshableAt returns the refresh time.
	RefreshableAt() int64
	// CASRefreshableAt executes the compare-and-swap operation for refreshableAt.
	CASRefreshableAt(old, new int64) bool
	// SetRefreshableAt returns the refresh time.
	SetRefreshableAt(new int64)
	IsFresh(now int64) bool
	// Weight returns the weight of the node.
	Weight() uint32
	// IsAlive returns true if the entry is available in the hash-table.
	IsAlive() bool
	// Die sets the node to the dead state.
	Die()
	// Frequency returns the frequency of the node.
	Frequency() uint8
	// IncrementFrequency increments the frequency of the node.
	IncrementFrequency()
	// DecrementFrequency decrements the frequency of the node.
	DecrementFrequency()
	// ResetFrequency resets the frequency.
	ResetFrequency()
	// MarkSmall sets the status to the small queue.
	MarkSmall()
	// IsSmall returns true if node is in the small queue.
	IsSmall() bool
	// MarkMain sets the status to the main queue.
	MarkMain()
	// IsMain returns true if node is in the main queue.
	IsMain() bool
	// Unmark sets the status to unknown.
	Unmark()
}

func Equals[K comparable, V any](a, b Node[K, V]) bool {
	if a == nil {
		return b == nil || b.AsPointer() == nil
	}
	if b == nil {
		return a.AsPointer() == nil
	}
	return a.AsPointer() == b.AsPointer()
}

type Config struct {
	WithSize       bool
	WithExpiration bool
	WithWeight     bool
	WithRefresh    bool
}

type Manager[K comparable, V any] struct {
	create      func(key K, value V, expiresAt, refreshableAt int64, weight uint32) Node[K, V]
	fromPointer func(ptr unsafe.Pointer) Node[K, V]
}

func NewManager[K comparable, V any](c Config) *Manager[K, V] {
	var sb strings.Builder
	sb.WriteString("b")
	if c.WithSize {
		sb.WriteString("s")
	}
	if c.WithExpiration {
		sb.WriteString("e")
	}
	if c.WithRefresh {
		sb.WriteString("r")
	}
	if c.WithWeight {
		sb.WriteString("w")
	}
	nodeType := sb.String()
	m := &Manager[K, V]{}
`

	const nodeFooter = `return m
}

func (m *Manager[K, V]) Create(key K, value V, expiresAt, refreshableAt int64, weight uint32) Node[K, V] {
	return m.create(key, value, expiresAt, refreshableAt, weight)
}

func (m *Manager[K, V]) FromPointer(ptr unsafe.Pointer) Node[K, V] {
	return m.fromPointer(ptr)
}

func (m *Manager[K, V]) IsNil(n Node[K, V]) bool {
	return n == nil || n.AsPointer() == nil
}

func minUint8(a, b uint8) uint8 {
	if a < b {
		return a
	}

	return b
}`
	w := newWriter()

	w.p(nodeManager)
	w.in()
	w.p("switch nodeType {")
	for _, nodeType := range nodeTypes {
		w.p("case \"%s\":", nodeType)
		w.in()
		structName := strings.ToUpper(nodeType)
		w.p("m.create = New%s[K, V]", structName)
		w.p("m.fromPointer = CastPointerTo%s[K, V]", structName)
		w.out()
	}
	w.p("default:")
	w.in()
	w.p("panic(\"not valid nodeType\")")
	w.out()
	w.p("}")
	w.p(nodeFooter)

	managerPath := filepath.Join(dir, "manager.go")
	f, err := os.Create(managerPath)
	if err != nil {
		return fmt.Errorf("create file %s: %w", managerPath, err)
	}
	defer f.Close()

	if _, err := f.Write(w.output()); err != nil {
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
		if err := run(nodeType, dir); err != nil {
			log.Fatal(err)
		}
	}

	if err := printManager(dir); err != nil {
		log.Fatal(err)
	}
}
