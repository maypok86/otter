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
	// emptyStorage keeps nothing: a value of zero size (struct{}, [0]T) has a single value.
	emptyStorage = "empty"
)

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

// A node of a feature set is allocated as one of the layouts below: the node type, which is the
// header and implements Node, followed by the value. The header of a node with state records its
// layout, so that the value accessors can find the value, while everything else only deals with
// the header: one node type per feature set, whatever the value storage.
type variant struct {
	// name is the suffix of the node type's name.
	name string
	// constant is the variant constant stored in the header.
	constant string
}

var (
	// inlineVariant keeps the value inline; the value is never changed.
	inlineVariant = variant{name: "Inline", constant: "inlineVariant"}
	// boxedVariant keeps the value behind an atomic pointer and replaces it in place. A node
	// with an inline value becomes one on its first update.
	boxedVariant = variant{name: "Boxed", constant: "boxedVariant"}
	// pointerVariant keeps a pointer-shaped value in an atomic pointer.
	pointerVariant = variant{name: "P", constant: "pointerVariant"}
	// wordVariant keeps a value without pointers of up to 8 bytes in an atomic.Uint64.
	wordVariant = variant{name: "U64", constant: "wordVariant"}
	// emptyVariant is a node without a value field, for values of zero size: the node is its
	// header alone, as large as a node of a cache that never updates in place.
	emptyVariant = variant{name: "Empty", constant: "emptyVariant"}
)

// allVariants lists the variants in the order of their constants.
var allVariants = []variant{inlineVariant, boxedVariant, pointerVariant, wordVariant, emptyVariant}

// variantsOf returns the layouts generated for nodeType: nodes without state are never updated in
// place and keep their value in the node type itself.
func variantsOf(nodeType string) []variant {
	if !hasState(getFeatures(nodeType)) {
		return []variant{inlineVariant}
	}
	return allVariants
}

func typeNameOf(nodeType string) string {
	return strings.ToUpper(nodeType)
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
