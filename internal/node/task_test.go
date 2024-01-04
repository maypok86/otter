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

package node

import "testing"

func TestTask(t *testing.T) {
	n := New[int, int](1, 2, 6, 4)

	addTask := NewAddTask(n, 4)
	if addTask.Node() != n || !addTask.IsAdd() || addTask.CostDiff() != 4 {
		t.Fatalf("not valid add task %+v", addTask)
	}

	deleteTask := NewDeleteTask(n)
	if deleteTask.Node() != n || !deleteTask.IsDelete() || deleteTask.CostDiff() != 0 {
		t.Fatalf("not valid delete task %+v", deleteTask)
	}

	costDiff := uint32(6)
	updateTask := NewUpdateTask(n, costDiff)
	if updateTask.Node() != n || !updateTask.IsUpdate() || updateTask.CostDiff() != costDiff {
		t.Fatalf("not valid update task %+v", updateTask)
	}

	clearTask := NewClearTask[int, int]()
	if clearTask.Node() != nil || !clearTask.IsClear() || clearTask.CostDiff() != 0 {
		t.Fatalf("not valid clear task %+v", clearTask)
	}

	closeTask := NewCloseTask[int, int]()
	if closeTask.Node() != nil || !closeTask.IsClose() || closeTask.CostDiff() != 0 {
		t.Fatalf("not valid close task %+v", closeTask)
	}
}
