// Package report renders portable, redacted views of a controlled efficiency
// cohort. It deliberately consumes only experiment summaries and never raw run
// artifacts, prompts or session exports.
package report

import (
	"bytes"
	"fmt"
	"html/template"
	"strings"

	"mbl/ocbench/internal/experiment"
)

// Markdown renders a deterministic, portable cohort report.
func Markdown(summary experiment.CohortSummary) []byte {
	var out bytes.Buffer
	fmt.Fprintln(&out, "# ocbench efficiency cohort")
	fmt.Fprintln(&out)
	fmt.Fprintf(&out, "- **Experiment:** %s\n", markdownCode(summary.Experiment.ID))
	fmt.Fprintf(&out, "- **Name:** %s\n", markdownText(summary.Experiment.Name))
	fmt.Fprintf(&out, "- **Recorded:** %s\n", markdownText(summary.Experiment.CreatedAt))
	fmt.Fprintf(&out, "- **Evidence:** %s\n", markdownText(summary.Gate))
	fmt.Fprintf(&out, "- **Runner environments:** %s\n", markdownText(orUnknown(summary.RunnerEnvironments)))
	fmt.Fprintln(&out)
	fmt.Fprintln(&out, "| Configuration | Profile | Tasks | Minimum repeats | Pass | Cost / solved | Median tokens | Environments | Eligible |")
	fmt.Fprintln(&out, "| --- | --- | ---: | ---: | ---: | ---: | ---: | --- | --- |")
	for _, arm := range summary.Arms {
		cost := "—"
		if arm.CostPerSolvedOK {
			cost = fmt.Sprintf("$%.6f", arm.CostPerSolved)
		}
		tokens := "—"
		if arm.MedianTokensOK {
			tokens = fmt.Sprintf("%.0f", arm.MedianTokens)
		}
		fmt.Fprintf(&out, "| %s | %s | %d | %d | %.0f%% | %s | %s | %s | %s |\n",
			markdownText(arm.Label), markdownCode(arm.ProfileHash), arm.TaskCount, arm.MinRepeats,
			arm.PassRate*100, cost, tokens, markdownText(orUnknown(arm.RunnerEnvironments)), yesNo(arm.Eligible))
	}
	if len(summary.DriftWarnings) > 0 {
		fmt.Fprintln(&out)
		fmt.Fprintln(&out, "## Compatibility warnings")
		for _, warning := range summary.DriftWarnings {
			fmt.Fprintf(&out, "- %s\n", markdownText(warning))
		}
	}
	return out.Bytes()
}

// HTML renders a self-contained version of the same report. html/template
// escapes every stored string; the CSS is inline so the file is safe to send
// without a CDN or another asset.
func HTML(summary experiment.CohortSummary) ([]byte, error) {
	t, err := template.New("report").Funcs(template.FuncMap{
		"join":   orUnknown,
		"mul100": func(value float64) float64 { return value * 100 },
	}).Parse(reportHTML)
	if err != nil {
		return nil, err
	}
	var out bytes.Buffer
	if err := t.Execute(&out, htmlView{Summary: summary}); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}

type htmlView struct{ Summary experiment.CohortSummary }

func (v htmlView) Environments() string { return orUnknown(v.Summary.RunnerEnvironments) }

func orUnknown(values []string) string {
	if len(values) == 0 {
		return "unknown"
	}
	return strings.Join(values, ", ")
}

func yesNo(value bool) string {
	if value {
		return "yes"
	}
	return "no"
}

func markdownText(value string) string {
	return strings.NewReplacer(
		"\\", "\\\\", "\n", " ", "`", "\\`", "*", "\\*", "_", "\\_",
		"{", "\\{", "}", "\\}", "[", "\\[", "]", "\\]", "<", "\\<", ">", "\\>",
		"(", "\\(", ")", "\\)", "#", "\\#", "+", "\\+", "-", "\\-", ".", "\\.", "!", "\\!", "|", "\\|",
	).Replace(value)
}

func markdownCode(value string) string {
	fence := "`"
	for strings.Contains(value, fence) {
		fence += "`"
	}
	return fence + value + fence
}

const reportHTML = `<!doctype html>
<html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1">
<title>ocbench efficiency cohort</title><style>
body{max-width:960px;margin:3rem auto;padding:0 1rem;background:#10110f;color:#e7e8e2;font:15px/1.5 ui-monospace,SFMono-Regular,Menlo,monospace}h1,h2{color:#b9f27c}code{color:#cbe9ff}table{width:100%;border-collapse:collapse;margin-top:1.5rem}th,td{padding:.45rem;border-bottom:1px solid #45483f;text-align:left;vertical-align:top}th{color:#a8aba1}.gate{border-left:3px solid #b9f27c;padding:.6rem 1rem;background:#1a1c18}.no{color:#ff928b}.yes{color:#b9f27c}.muted{color:#a8aba1}</style></head>
<body><h1>ocbench efficiency cohort</h1>
<p class="muted">Experiment <code>{{.Summary.Experiment.ID}}</code> · {{.Summary.Experiment.CreatedAt}}</p>
<p>{{.Summary.Experiment.Name}}</p>
<div class="gate"><strong>Evidence:</strong> {{.Summary.Gate}}<br><strong>Runner environments:</strong> {{.Environments}}</div>
<table><thead><tr><th>Configuration</th><th>Profile</th><th>Tasks</th><th>Min repeats</th><th>Pass</th><th>Cost / solved</th><th>Median tokens</th><th>Environments</th><th>Eligible</th></tr></thead><tbody>
{{range .Summary.Arms}}<tr><td>{{.Label}}</td><td><code>{{.ProfileHash}}</code></td><td>{{.TaskCount}}</td><td>{{.MinRepeats}}</td><td>{{printf "%.0f%%" (mul100 .PassRate)}}</td><td>{{if .CostPerSolvedOK}}{{printf "$%.6f" .CostPerSolved}}{{else}}—{{end}}</td><td>{{if .MedianTokensOK}}{{printf "%.0f" .MedianTokens}}{{else}}—{{end}}</td><td>{{join .RunnerEnvironments}}</td><td class="{{if .Eligible}}yes{{else}}no{{end}}">{{if .Eligible}}yes{{else}}no{{end}}</td></tr>{{end}}
</tbody></table>{{if .Summary.DriftWarnings}}<h2>Compatibility warnings</h2><ul>{{range .Summary.DriftWarnings}}<li>{{.}}</li>{{end}}</ul>{{end}}</body></html>`
