package node

import "testing"

func TestTask(t *testing.T) {
	n := New[int, int](1, 2, 6, 4)

	addTask := NewAddTask(n)
	if addTask.GetNode() != n || !addTask.IsAdd() || addTask.GetCostDiff() != 0 {
		t.Fatalf("not valid add task %+v", addTask)
	}

	deleteTask := NewDeleteTask(n)
	if deleteTask.GetNode() != n || !deleteTask.IsDelete() || deleteTask.GetCostDiff() != 0 {
		t.Fatalf("not valid delete task %+v", deleteTask)
	}

	costDiff := uint32(6)
	updateTask := NewUpdateTask(n, costDiff)
	if updateTask.GetNode() != n || !updateTask.IsUpdate() || updateTask.GetCostDiff() != costDiff {
		t.Fatalf("not valid update task %+v", updateTask)
	}

	clearTask := NewClearTask[int, int]()
	if clearTask.GetNode() != nil || !clearTask.IsClear() || clearTask.GetCostDiff() != 0 {
		t.Fatalf("not valid clear task %+v", clearTask)
	}

	closeTask := NewCloseTask[int, int]()
	if closeTask.GetNode() != nil || !closeTask.IsClose() || closeTask.GetCostDiff() != 0 {
		t.Fatalf("not valid close task %+v", closeTask)
	}
}
