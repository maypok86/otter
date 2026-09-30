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
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

type generator struct {
	*writer

	// structName is the node type of the feature set, the header of all its layouts, which
	// implements Node.
	structName string
	features   map[feature]bool
	variants   []variant
}

func newGenerator(nodeType string) *generator {
	return &generator{
		writer:     newWriter(),
		structName: typeNameOf(nodeType),
		features:   getFeatures(nodeType),
		variants:   variantsOf(nodeType),
	}
}

func (g *generator) variantName(v variant) string {
	return g.structName + v.name
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
	if g.withState() {
		g.p("//")
		g.p("// It is the header of the layouts that store the value (%s...): a node is allocated", g.variantName(g.variants[0]))
		g.p("// as one of them, whose first field is the header, so that a pointer to the header is a")
		g.p("// pointer to the whole node. The layouts do not embed the header: they have no methods,")
		g.p("// which keeps the code of one node type per feature set.")
	}
}

func (g *generator) printStruct() {
	g.printStructComment()

	g.p("type %s[K comparable, V any] struct {", g.structName)
	g.in()
	g.p("key        K")
	if !g.withState() {
		// never updated in place, so the value is kept in the node itself, next to the key
		g.p("value      V")
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
	if g.withState() {
		// set when the node is created and never changed: the layout the node was allocated
		// as. It is a separate byte, never packed with queueType, which the policy writes
		// without atomics.
		g.p("variant    uint8")
	}
	g.out()
	g.p("}")
	g.p("")

	if !g.withState() {
		return
	}
	for _, v := range g.variants {
		if v == emptyVariant {
			// allocated as the header alone
			continue
		}
		name := g.variantName(v)
		switch v {
		case inlineVariant:
			g.p("// %s is the layout of a %s whose value is kept inline and never changed. The first", name, g.structName)
			g.p("// update of a live entry replaces the node with a %s.", g.variantName(boxedVariant))
			g.p("type %s[K comparable, V any] struct {", name)
			g.in()
			g.p("header %s[K, V]", g.structName)
			g.p("value V")
		case boxedVariant:
			g.p("// %s is the layout of a %s whose value lives behind an atomic pointer and is", name, g.structName)
			g.p("// replaced in place.")
			g.p("type %s[K comparable, V any] struct {", name)
			g.in()
			g.p("header %s[K, V]", g.structName)
			g.p("value atomic.Pointer[V]")
		case pointerVariant:
			g.p("// %s is the layout of a %s whose pointer-shaped value is kept in an atomic pointer.", name, g.structName)
			g.p("type %s[K comparable, V any] struct {", name)
			g.in()
			g.p("header %s[K, V]", g.structName)
			// accessed only atomically; holds the value's pointer word, so the GC sees it
			g.p("value unsafe.Pointer")
		case wordVariant:
			g.p("// %s is the layout of a %s whose value, without pointers and of at most 8 bytes,", name, g.structName)
			g.p("// is kept in an atomic.Uint64.")
			g.p("type %s[K comparable, V any] struct {", name)
			g.in()
			g.p("header %s[K, V]", g.structName)
			g.p("value atomic.Uint64")
		default:
			panic(fmt.Sprintf("unknown variant %v", v))
		}
		g.out()
		g.p("}")
		g.p("")
	}
}

func (g *generator) printConstructors() {
	g.p("// New%s creates a new %s allocated as the given variant's layout (see variantsOf in the", g.structName, g.structName)
	g.p("// generator); nodes without state have a single layout and ignore it.")
	g.p("func New%s[K comparable, V any](key K, value V, expiresAt, refreshableAt int64, weight uint32, variant uint8) Node[K, V] {", g.structName)
	g.in()
	if g.withState() {
		g.p("var n *%s[K, V]", g.structName)
		g.p("switch variant {")
		for _, v := range g.variants[1:] {
			g.p("case %s:", v.constant)
			g.in()
			if v == emptyVariant {
				g.p("n = &%s[K, V]{}", g.structName)
			} else {
				g.p("n = &(&%s[K, V]{}).header", g.variantName(v))
			}
			g.out()
		}
		g.p("default:")
		g.in()
		g.p("n = &(&%s[K, V]{}).header", g.variantName(g.variants[0]))
		g.out()
		g.p("}")
		g.p("n.key = key")
		g.p("n.variant = variant")
		g.p("n.SetValue(value)")
	} else {
		g.p("n := &%s[K, V]{", g.structName)
		g.in()
		g.p("key: key,")
		g.p("value: value,")
		g.out()
		g.p("}")
	}
	if g.features[weight] {
		g.p("n.policyWeight = weight")
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

// printValueFunctions prints the value accessors. They convert the header back to the layout the
// node was allocated as, whose pointer it was taken from.
func (g *generator) printValueFunctions() {
	cast := func(v variant) string {
		return fmt.Sprintf("(*%s[K, V])(unsafe.Pointer(n))", g.variantName(v))
	}
	// the inline variant is the default of every switch
	byVariant := func(emit func(v variant)) {
		g.p("switch n.variant {")
		for _, v := range g.variants[1:] {
			g.p("case %s:", v.constant)
			g.in()
			emit(v)
			g.out()
		}
		g.p("default:")
		g.in()
		emit(g.variants[0])
		g.out()
		g.p("}")
	}

	if !g.withState() {
		g.p("func (n *%s[K, V]) Value() V {", g.structName)
		g.in()
		g.p("return n.value")
		g.out()
		g.p("}")
		g.p("")
		g.p("func (n *%s[K, V]) SetValue(v V) {", g.structName)
		g.in()
		g.p("panic(\"otter: a node without state is never updated in place\")")
		g.out()
		g.p("}")
		g.p("")
		g.p("func (n *%s[K, V]) CanSetValue() bool {", g.structName)
		g.in()
		g.p("return false")
		g.out()
		g.p("}")
		g.p("")
		return
	}

	g.p("func (n *%s[K, V]) Value() V {", g.structName)
	g.in()
	byVariant(func(v variant) {
		switch v {
		case inlineVariant:
			g.p("return %s.value", cast(v))
		case boxedVariant:
			g.p("return *%s.value.Load()", cast(v))
		case pointerVariant:
			g.p("p := atomic.LoadPointer(&%s.value)", cast(v))
			g.p("return *(*V)(unsafe.Pointer(&p))")
		case wordVariant:
			g.p("w := %s.value.Load()", cast(v))
			g.p("return *(*V)(unsafe.Pointer(&w))")
		case emptyVariant:
			g.p("var zero V")
			g.p("return zero")
		default:
			panic(fmt.Sprintf("unknown variant %v", v))
		}
	})
	g.out()
	g.p("}")
	g.p("")

	// SetValue also stores the value of a new node; later, it is only called when CanSetValue
	// (see the Node interface)
	g.p("func (n *%s[K, V]) SetValue(v V) {", g.structName)
	g.in()
	byVariant(func(v variant) {
		switch v {
		case inlineVariant:
			g.p("%s.value = v", cast(v))
		case boxedVariant:
			g.p("n.setBoxedValue(v)")
		case pointerVariant:
			g.p("atomic.StorePointer(&%s.value, *(*unsafe.Pointer)(unsafe.Pointer(&v)))", cast(v))
		case wordVariant:
			g.p("var w uint64")
			g.p("*(*V)(unsafe.Pointer(&w)) = v")
			g.p("%s.value.Store(w)", cast(v))
		case emptyVariant:
			g.p("// a value of zero size has nothing to store")
		default:
			panic(fmt.Sprintf("unknown variant %v", v))
		}
	})
	g.out()
	g.p("}")
	g.p("")

	if slices.Contains(g.variants, boxedVariant) {
		// Separate from SetValue: escape analysis works per function, and the pointer taken to
		// the value here would otherwise move the value of every SetValue call to the heap.
		g.p("func (n *%s[K, V]) setBoxedValue(v V) {", g.structName)
		g.in()
		g.p("%s.value.Store(&v)", cast(boxedVariant))
		g.out()
		g.p("}")
		g.p("")
	}

	g.p("func (n *%s[K, V]) CanSetValue() bool {", g.structName)
	g.in()
	g.p("return n.variant != %s", inlineVariant.constant)
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
	g.printValueFunctions()

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
