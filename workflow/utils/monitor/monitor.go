package monitor

import (
	"fmt"
	"iter"
	"strings"
	"time"

	"github.com/fatih/color"
	"github.com/inancgumus/screen"
	"github.com/kylelemons/godebug/pretty"
	"github.com/rodaine/table"

	"github.com/element-of-surprise/coercion"
	"github.com/element-of-surprise/coercion/workflow"
)

var pConfig = pretty.Config{
	IncludeUnexported: false,
	PrintStringers:    true,
	SkipZeroFields:    true,
}

// Monitor is a function that listens to the workflow results iterator and prints a running summary.
func Monitor(results iter.Seq[coercion.Result[*workflow.Plan]]) coercion.Result[*workflow.Plan] {
	var last *workflow.Plan
	var result coercion.Result[*workflow.Plan]
	for result = range results {
		if result.Err != nil {
			panic(result.Err)
		}

		diff := pConfig.Compare(last, result.Data)
		last = result.Data
		if diff == "" {
			continue
		}

		screen.Clear()
		screen.MoveTopLeft()

		fmt.Println(runningSummary(result.Data))
	}
	return result
}

func runningSummary(p *workflow.Plan) string {
	if len(p.Blocks) == 0 {
		return "no blocks defined"
	}

	blockTitle := color.New(color.FgCyan).Add(color.Underline)
	name := color.New(color.FgGreen)
	desc := color.New(color.FgYellow)

	buff := strings.Builder{}
	buff.WriteString(fmt.Sprintf("Time: %s\n", time.Now().Format(time.RFC1123)))
	buff.WriteString(fmt.Sprintf("Workflow: %s\n", p.ID))
	name.Fprintln(&buff, "Name: "+p.Name)
	desc.Fprintln(&buff, "Description: "+p.Descr)

	blockIndex, block := findRunningBlock(p.Blocks)
	if block == nil {
		return ""
	}
	seqs := findRunningSeq(block.Sequences)

	blockTitle.Fprintln(&buff, "\nBlock Summaries")
	writeOtherBlocks(&buff, p.Blocks)

	blockTitle.Fprintln(&buff, fmt.Sprintf("\nRunning Block(%d): %s", blockIndex, block.Name))
	writeRunningBlock(&buff, block)

	for _, seq := range seqs {
		blockTitle.Fprintln(&buff, fmt.Sprintf("\nRunning Sequence Actions: %s", seq.Name))
		writeRunningActions(&buff, seq)
	}

	return buff.String()
}

func writeOtherBlocks(buff *strings.Builder, blocks []*workflow.Block) {
	headerFmt := color.New(color.FgGreen, color.Underline).SprintfFunc()
	columnFmt := color.New(color.FgYellow).SprintfFunc()

	tbl := table.New("Block Number", "Desc", "Status").WithWriter(buff)
	tbl.WithHeaderFormatter(headerFmt).WithFirstColumnFormatter(columnFmt)

	for i, block := range blocks {
		tbl.AddRow(i, block.Descr, block.State.Status)
		if block.State.Status == workflow.Running {
			continue
		}
	}
	tbl.Print()
}

func findRunningBlock(blocks []*workflow.Block) (int, *workflow.Block) {
	for i, b := range blocks {
		if b.State.Status == workflow.Running {
			return i, b
		}
	}
	return -1, nil
}

func findRunningSeq(seq []*workflow.Sequence) []*workflow.Sequence {
	var found []*workflow.Sequence
	for _, s := range seq {
		if s.State.Status == workflow.Running {
			found = append(found, s)
		}
	}
	return found
}

func writeRunningBlock(buff *strings.Builder, block *workflow.Block) {
	headerFmt := color.New(color.FgGreen, color.Underline).SprintfFunc()
	columnFmt := color.New(color.FgYellow).SprintfFunc()

	tbl := table.New("Seq Number", "Desc", "Status").WithWriter(buff)
	tbl.WithHeaderFormatter(headerFmt).WithFirstColumnFormatter(columnFmt)

	for i, seq := range block.Sequences {
		tbl.AddRow(i, seq.Name, seq.State.Status)
	}
	tbl.Print()
}

func writeRunningActions(buff *strings.Builder, seq *workflow.Sequence) {
	headerFmt := color.New(color.FgGreen, color.Underline).SprintfFunc()
	columnFmt := color.New(color.FgYellow).SprintfFunc()

	tbl := table.New("Action Number", "Name", "Status").WithWriter(buff)
	tbl.WithHeaderFormatter(headerFmt).WithFirstColumnFormatter(columnFmt)

	for i, action := range seq.Actions {
		tbl.AddRow(i, action.Name, action.State.Status)
	}
	tbl.Print()
}
