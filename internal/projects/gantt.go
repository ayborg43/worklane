package projects

import (
	"encoding/json"
	"strconv"
	"strings"
)

type GanttTask struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	Start        string `json:"start"`
	End          string `json:"end"`
	Progress     int    `json:"progress"`
	Dependencies string `json:"dependencies"`
}

// BuildGanttTasksJSON maps tasks (with their dependency edges already
// populated) into the flat {id,name,start,end,progress,dependencies} shape
// frappe-gantt expects, and marshals it to JSON. encoding/json HTML-escapes
// <, >, and & by default, so this is safe to embed directly inside a
// <script> tag.
func BuildGanttTasksJSON(tasks []Task) ([]byte, error) {
	out := make([]GanttTask, 0, len(tasks))
	for _, t := range tasks {
		depStrs := make([]string, len(t.DependsOn))
		for i, d := range t.DependsOn {
			depStrs[i] = strconv.FormatInt(d, 10)
		}
		out = append(out, GanttTask{
			ID:           strconv.FormatInt(t.ID, 10),
			Name:         t.Name,
			Start:        t.StartDate.Format("2006-01-02"),
			End:          t.EndDate.Format("2006-01-02"),
			Progress:     t.Progress,
			Dependencies: strings.Join(depStrs, ","),
		})
	}
	return json.Marshal(out)
}
