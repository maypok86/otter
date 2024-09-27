// Copyright (c) 2023 Alexey Mayshev and contributors. All rights reserved.
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
	"testing"

	"github.com/maypok86/otter/v2/internal/generated/node"
)

func TestTask(t *testing.T) {
	nm := node.NewManager[int, int](node.Config{
		WithExpiration: true,
		WithWeight:     true,
	})
	n := nm.Create(1, 2, 6, 4)
	oldNode := nm.Create(1, 3, 8, 6)

	addTask := newAddTask(n)
	if addTask.node() != n || !addTask.isAdd() {
		t.Fatalf("not valid add task %+v", addTask)
	}

	deleteTask := newDeleteTask(n, CauseInvalidation)
	if deleteTask.node() != n || !deleteTask.isDelete() {
		t.Fatalf("not valid delete task %+v", deleteTask)
	}

	updateTask := newUpdateTask(n, oldNode, CauseExpiration)
	if updateTask.node() != n || !updateTask.isUpdate() || updateTask.oldNode() != oldNode {
		t.Fatalf("not valid update task %+v", updateTask)
	}

	clearTask := newClearTask[int, int]()
	if clearTask.node() != nil || !clearTask.isClear() {
		t.Fatalf("not valid clear task %+v", clearTask)
	}

	closeTask := newCloseTask[int, int]()
	if closeTask.node() != nil || !closeTask.isClose() {
		t.Fatalf("not valid close task %+v", closeTask)
	}
}
