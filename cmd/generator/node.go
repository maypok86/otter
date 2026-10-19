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
	g.p("// %s is the header of a cache entry that provide the following features:", g.structName)
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
	g.p("//")
	g.p("// A node is allocated as one of the layouts that follow the header with the key and the")
	g.p("// value (%s...), so that a pointer to the header is a pointer to the whole node. The", g.variantName(g.variants[0]))
	g.p("// header does not depend on the key and value types: its methods are compiled once,")
	g.p("// whatever caches a program creates, and only the layouts are instantiated per cache type.")
}

func (g *generator) printStruct() {
	g.printStructComment()

	g.p("type %s struct {", g.structName)
	g.in()
	if g.isBounded() {
		g.p("prev       *%s", g.structName)
		g.p("next       *%s", g.structName)
	}
	if g.features[expiration] {
		g.p("prevExp    *%s", g.structName)
		g.p("nextExp    *%s", g.structName)
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

	for _, v := range g.variants {
		name := g.variantName(v)
		switch v {
		case inlineVariant:
			if g.withState() {
				g.p("// %s is the layout of a %s whose value is kept inline and never changed. The first", name, g.structName)
				g.p("// update of a live entry replaces the node with a %s.", g.variantName(boxedVariant))
			} else {
				g.p("// %s is the layout of a %s, which is never updated in place.", name, g.structName)
			}
			g.p("type %s[K comparable, V any] struct {", name)
			g.in()
			g.p("header %s", g.structName)
			g.p("key K")
			g.p("value V")
		case boxedVariant:
			g.p("// %s is the layout of a %s whose value lives behind an atomic pointer and is", name, g.structName)
			g.p("// replaced in place.")
			g.p("type %s[K comparable, V any] struct {", name)
			g.in()
			g.p("header %s", g.structName)
			g.p("key K")
			g.p("value atomic.Pointer[V]")
		case pointerVariant:
			g.p("// %s is the layout of a %s whose pointer-shaped value is kept in an atomic pointer.", name, g.structName)
			g.p("type %s[K comparable, V any] struct {", name)
			g.in()
			g.p("header %s", g.structName)
			g.p("key K")
			// accessed only atomically; holds the value's pointer word, so the GC sees it
			g.p("value unsafe.Pointer")
		case wordVariant:
			g.p("// %s is the layout of a %s whose value, without pointers and of at most 8 bytes,", name, g.structName)
			g.p("// is kept in an atomic.Uint64.")
			g.p("type %s[K comparable, V any] struct {", name)
			g.in()
			g.p("header %s", g.structName)
			g.p("key K")
			g.p("value atomic.Uint64")
		case emptyVariant:
			g.p("// %s is the layout of a %s without a value field, for values of zero size.", name, g.structName)
			g.p("type %s[K comparable, V any] struct {", name)
			g.in()
			g.p("header %s", g.structName)
			g.p("key K")
		default:
			panic(fmt.Sprintf("unknown variant %v", v))
		}
		g.out()
		g.p("}")
		g.p("")
	}
}

func (g *generator) printConstructors() {
	lower := strings.ToLower(g.structName)

	// alloc and layout are the only functions instantiated per cache type
	g.p("// alloc%s allocates a node as the given variant's layout and returns its header; nodes", g.structName)
	g.p("// without state have a single layout and ignore the variant.")
	g.p("func alloc%s[K comparable, V any](variant uint8) unsafe.Pointer {", g.structName)
	g.in()
	if g.withState() {
		g.p("switch variant {")
		for _, v := range g.variants[1:] {
			g.p("case %s:", v.constant)
			g.in()
			g.p("return unsafe.Pointer(new(%s[K, V]))", g.variantName(v))
			g.out()
		}
		g.p("default:")
		g.in()
		g.p("return unsafe.Pointer(new(%s[K, V]))", g.variantName(g.variants[0]))
		g.out()
		g.p("}")
	} else {
		g.p("return unsafe.Pointer(new(%s[K, V]))", g.variantName(g.variants[0]))
	}
	g.out()
	g.p("}")
	g.p("")

	g.p("// layout%s returns the offsets of the key and of the value of every variant's layout.", g.structName)
	g.p("func layout%s[K comparable, V any]() (key uintptr, values [variantCount]uintptr) {", g.structName)
	g.in()
	g.p("key = unsafe.Offsetof(%s[K, V]{}.key)", g.variantName(g.variants[0]))
	for _, v := range g.variants {
		if v == emptyVariant {
			continue
		}
		g.p("values[%s] = unsafe.Offsetof(%s[K, V]{}.value)", v.constant, g.variantName(v))
	}
	g.p("return key, values")
	g.out()
	g.p("}")
	g.p("")

	g.p("// init%s initializes the header of a node allocated by alloc%s.", g.structName, g.structName)
	g.p("func init%s(ptr unsafe.Pointer, expiresAt, refreshableAt int64, weight uint32, variant uint8) Node {", g.structName)
	g.in()
	g.p("n := (*%s)(ptr)", g.structName)
	if g.withState() {
		g.p("n.variant = variant")
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
	g.p("func CastPointerTo%s(ptr unsafe.Pointer) Node {", g.structName)
	g.in()
	g.p("if ptr == nil {")
	g.in()
	g.p("return nil")
	g.out()
	g.p("}")
	g.p("return (*%s)(ptr)", g.structName)
	g.out()
	g.p("}")
	g.p("")

	g.p("var %sKind = Kind{", lower)
	g.in()
	g.p("fromPointer: CastPointerTo%s,", g.structName)
	g.p("init:        init%s,", g.structName)
	g.p("newHeader:   func() unsafe.Pointer { return unsafe.Pointer(new(%s)) },", g.structName)
	if g.withState() {
		g.p("variantOffset: unsafe.Offsetof(%s{}.variant),", g.structName)
		g.p("withState:   true,")
	}
	if g.isBounded() {
		g.p("linksOffset: unsafe.Offsetof(%s{}.prev),", g.structName)
		g.p("hasLinks:    true,")
	}
	if g.features[expiration] {
		g.p("expLinksOffset: unsafe.Offsetof(%s{}.prevExp),", g.structName)
		g.p("hasExpLinks:    true,")
	}
	g.out()
	g.p("}")
	g.p("")
}

func (g *generator) printValueFunctions() {
	g.p("func (n *%s) CanSetValue() bool {", g.structName)
	g.in()
	if g.withState() {
		g.p("return n.variant != %s", inlineVariant.constant)
	} else {
		g.p("return false")
	}
	g.out()
	g.p("}")
	g.p("")
}

func (g *generator) printFunctions() {
	g.p("func (n *%s) AsPointer() unsafe.Pointer {", g.structName)
	g.in()
	g.p("return unsafe.Pointer(n)")
	g.out()
	g.p("}")
	g.p("")

	g.p("func (n *%s) Prev() Node {", g.structName)
	g.in()
	if g.isBounded() {
		// A nil field is returned as a nil interface, not as a typed nil, so that callers
		// can compare nodes with ==.
		g.p("if n.prev == nil {")
		g.in()
		g.p("return nil")
		g.out()
		g.p("}")
		g.p("return n.prev")
	} else {
		g.p("panic(\"not implemented\")")
	}
	g.out()
	g.p("}")
	g.p("")

	g.p("func (n *%s) SetPrev(v Node) {", g.structName)
	g.in()
	if g.isBounded() {
		g.p("if v == nil {")
		g.in()
		g.p("n.prev = nil")
		g.p("return")
		g.out()
		g.p("}")
		g.p("n.prev = (*%s)(v.AsPointer())", g.structName)
	} else {
		g.p("panic(\"not implemented\")")
	}
	g.out()
	g.p("}")
	g.p("")

	g.p("func (n *%s) Next() Node {", g.structName)
	g.in()
	if g.isBounded() {
		g.p("if n.next == nil {")
		g.in()
		g.p("return nil")
		g.out()
		g.p("}")
		g.p("return n.next")
	} else {
		g.p("panic(\"not implemented\")")
	}
	g.out()
	g.p("}")
	g.p("")

	g.p("func (n *%s) SetNext(v Node) {", g.structName)
	g.in()
	if g.isBounded() {
		g.p("if v == nil {")
		g.in()
		g.p("n.next = nil")
		g.p("return")
		g.out()
		g.p("}")
		g.p("n.next = (*%s)(v.AsPointer())", g.structName)
	} else {
		g.p("panic(\"not implemented\")")
	}
	g.out()
	g.p("}")
	g.p("")

	g.p("func (n *%s) HasExpired(now int64) bool {", g.structName)
	g.in()
	if g.features[expiration] {
		g.p("return n.ExpiresAt() <= now")
	} else {
		g.p("return false")
	}
	g.out()
	g.p("}")
	g.p("")

	g.p("func (n *%s) ExpiresAt() int64 {", g.structName)
	g.in()
	if g.features[expiration] {
		g.p("return n.expiresAt.Load()")
	} else {
		g.p("panic(\"not implemented\")")
	}
	g.out()
	g.p("}")
	g.p("")

	g.p("func (n *%s) CASExpiresAt(old, new int64) bool {", g.structName)
	g.in()
	if g.features[expiration] {
		g.p("return n.expiresAt.CompareAndSwap(old, new)")
	} else {
		g.p("panic(\"not implemented\")")
	}
	g.out()
	g.p("}")
	g.p("")

	g.p("func (n *%s) SetExpiresAt(new int64) {", g.structName)
	g.in()
	if g.features[expiration] {
		g.p("n.expiresAt.Store(new)")
	} else {
		g.p("panic(\"not implemented\")")
	}
	g.out()
	g.p("}")
	g.p("")

	g.p("func (n *%s) RefreshableAt() int64 {", g.structName)
	g.in()
	if g.features[refresh] {
		g.p("return n.refreshableAt.Load()")
	} else {
		g.p("panic(\"not implemented\")")
	}
	g.out()
	g.p("}")
	g.p("")

	g.p("func (n *%s) CASRefreshableAt(old, new int64) bool {", g.structName)
	g.in()
	if g.features[refresh] {
		g.p("return n.refreshableAt.CompareAndSwap(old, new)")
	} else {
		g.p("panic(\"not implemented\")")
	}
	g.out()
	g.p("}")
	g.p("")

	g.p("func (n *%s) SetRefreshableAt(new int64) {", g.structName)
	g.in()
	if g.features[refresh] {
		g.p("n.refreshableAt.Store(new)")
	} else {
		g.p("panic(\"not implemented\")")
	}
	g.out()
	g.p("}")
	g.p("")

	g.p("func (n *%s) IsFresh(now int64) bool {", g.structName)
	g.in()
	if g.features[refresh] {
		g.p("return n.IsAlive() && n.RefreshableAt() > now")
	} else {
		g.p("return true")
	}
	g.out()
	g.p("}")
	g.p("")

	g.p("func (n *%s) Weight() uint32 {", g.structName)
	g.in()
	if g.features[weight] {
		g.p("return n.weight.Load()")
	} else {
		g.p("return 1")
	}
	g.out()
	g.p("}")
	g.p("")

	g.p("func (n *%s) SetWeight(weight uint32) {", g.structName)
	g.in()
	if g.features[weight] {
		g.p("n.weight.Store(weight)")
	} else {
		g.p("panic(\"not implemented\")")
	}
	g.out()
	g.p("}")
	g.p("")

	g.p("func (n *%s) PolicyWeight() uint32 {", g.structName)
	g.in()
	if g.features[weight] {
		g.p("return n.policyWeight")
	} else {
		g.p("return 1")
	}
	g.out()
	g.p("}")
	g.p("")

	g.p("func (n *%s) SetPolicyWeight(weight uint32) {", g.structName)
	g.in()
	if g.features[weight] {
		g.p("n.policyWeight = weight")
	}
	g.out()
	g.p("}")
	g.p("")

	g.p("func (n *%s) IsAlive() bool {", g.structName)
	g.in()
	if g.withState() {
		g.p("return n.state.Load() == aliveState")
	} else {
		g.p("return true")
	}
	g.out()
	g.p("}")
	g.p("")

	g.p("func (n *%s) IsRetired() bool {", g.structName)
	g.in()
	if g.withState() {
		g.p("return n.state.Load() == retiredState")
	} else {
		g.p("panic(\"not implemented\")")
	}
	g.out()
	g.p("}")
	g.p("")

	g.p("func (n *%s) Retire() {", g.structName)
	g.in()
	if g.withState() {
		g.p("n.state.Store(retiredState)")
	} else {
		g.p("panic(\"not implemented\")")
	}
	g.out()
	g.p("}")
	g.p("")

	g.p("func (n *%s) IsDead() bool {", g.structName)
	g.in()
	if g.withState() {
		g.p("return n.state.Load() == deadState")
	} else {
		g.p("panic(\"not implemented\")")
	}
	g.out()
	g.p("}")
	g.p("")

	g.p("func (n *%s) Die() {", g.structName)
	g.in()
	if g.withState() {
		g.p("n.state.Store(deadState)")
	} else {
		g.p("panic(\"not implemented\")")
	}
	g.out()
	g.p("}")
	g.p("")

	g.p("func (n *%s) GetQueueType() uint8 {", g.structName)
	g.in()
	if g.isBounded() {
		g.p("return n.queueType")
	} else {
		g.p("panic(\"not implemented\")")
	}
	g.out()
	g.p("}")
	g.p("")

	g.p("func (n *%s) SetQueueType(queueType uint8) {", g.structName)
	g.in()
	if g.isBounded() {
		g.p("n.queueType = queueType")
	} else {
		g.p("panic(\"not implemented\")")
	}
	g.out()
	g.p("}")
	g.p("")

	g.p("func (n *%s) InWindow() bool {", g.structName)
	g.in()
	g.p("return n.GetQueueType() == InWindowQueue")
	g.out()
	g.p("}")
	g.p("")

	g.p("func (n *%s) MakeWindow() {", g.structName)
	g.in()
	g.p("n.SetQueueType(InWindowQueue)")
	g.out()
	g.p("}")
	g.p("")

	g.p("func (n *%s) InMainProbation() bool {", g.structName)
	g.in()
	g.p("return n.GetQueueType() == InMainProbationQueue")
	g.out()
	g.p("}")
	g.p("")

	g.p("func (n *%s) MakeMainProbation() {", g.structName)
	g.in()
	g.p("n.SetQueueType(InMainProbationQueue)")
	g.out()
	g.p("}")
	g.p("")

	g.p("func (n *%s) InMainProtected() bool {", g.structName)
	g.in()
	g.p("return n.GetQueueType() == InMainProtectedQueue")
	g.out()
	g.p("}")
	g.p("")

	g.p("func (n *%s) MakeMainProtected() {", g.structName)
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
