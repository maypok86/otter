// Copyright (c) 2023 Alexey Mayshev. All rights reserved.
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

package task

import (
	"testing"

	"github.com/maypok86/otter/internal/generated/node"
)

func TestTask(t *testing.T) {
	nm := node.NewManager[int, int](node.Config{
		WithExpiration: true,
		WithCost:       true,
	})
	n := nm.Create(1, 2, 6, 4)
	oldNode := nm.Create(1, 3, 8, 6)

	addTask := NewAddTask(n)
	if addTask.Node() != n || !addTask.IsAdd() {
		t.Fatalf("not valid add task %+v", addTask)
	}

	deleteTask := NewDeleteTask(n)
	if deleteTask.Node() != n || !deleteTask.IsDelete() {
		t.Fatalf("not valid delete task %+v", deleteTask)
	}

	updateTask := NewUpdateTask(n, oldNode)
	if updateTask.Node() != n || !updateTask.IsUpdate() || updateTask.OldNode() != oldNode {
		t.Fatalf("not valid update task %+v", updateTask)
	}

	clearTask := NewClearTask[int, int]()
	if clearTask.Node() != nil || !clearTask.IsClear() {
		t.Fatalf("not valid clear task %+v", clearTask)
	}

	closeTask := NewCloseTask[int, int]()
	if closeTask.Node() != nil || !closeTask.IsClose() {
		t.Fatalf("not valid close task %+v", closeTask)
	}
}
