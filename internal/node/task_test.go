package node

import "testing"

func TestTask(t *testing.T) {
	n := New[int, int](1, 2, 6, 4)

	addTask := NewAddTask(n)
	if addTask.Node() != n || !addTask.IsAdd() || addTask.CostDiff() != 0 {
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
