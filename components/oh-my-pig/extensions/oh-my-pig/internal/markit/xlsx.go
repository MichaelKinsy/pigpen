package markit

import (
	"context"
	"fmt"
	"strconv"
	"strings"
)

var (
	xlsxExtensions = []string{".xlsx"}
	xlsxMIMETypes  = []string{"application/vnd.openxmlformats-officedocument.spreadsheetml.sheet"}
)

// XlsxConverter converts OOXML Excel workbooks (.xlsx) to Markdown tables.
// upstream: packages/coding-agent/src/markit/converters/xlsx.ts:1-180
type XlsxConverter struct{}

func (x *XlsxConverter) Name() string { return "xlsx" }

func (x *XlsxConverter) Accepts(info StreamInfo) bool {
	return matchesExtensionOrMIME(info, xlsxExtensions, xlsxMIMETypes)
}

func (x *XlsxConverter) Convert(ctx context.Context, input []byte, _ StreamInfo) (Result, error) {
	if err := ctx.Err(); err != nil {
		return Result{}, err
	}
	pkg, err := openZip(input)
	if err != nil {
		return Result{}, err
	}

	// Parse shared strings (optional).
	var shared []string
	if ssXML, ok, err := pkg.readText("xl/sharedStrings.xml"); err != nil {
		return Result{}, err
	} else if ok && ssXML != "" {
		if ssRoot, err := parseXML(ctx, ssXML); err == nil {
			for _, si := range ssRoot.childElements("si") {
				shared = append(shared, extractXlsxStringItem(si))
			}
		} else if ctx.Err() != nil {
			return Result{}, ctx.Err()
		}
	}

	// Parse workbook.xml for sheet names and rIds.
	wbXML, ok, err := pkg.readText("xl/workbook.xml")
	if err != nil {
		return Result{}, err
	}
	if !ok || wbXML == "" {
		return Result{}, fmt.Errorf("Invalid XLSX: missing workbook.xml")
	}
	wbRoot, err := parseXML(ctx, wbXML)
	if err != nil {
		return Result{}, fmt.Errorf("Invalid XLSX: %w", err)
	}
	sheetsNode := wbRoot.firstChild("sheets")
	sheets := sheetsNode.childElements("sheet")

	// Parse xl/_rels/workbook.xml.rels to map rIds to worksheet paths.
	relMap := parseSimpleRelationships(ctx, pkg, "xl/_rels/workbook.xml.rels")

	var sections []string
	for _, sheet := range sheets {
		if err := ctx.Err(); err != nil {
			return Result{}, err
		}
		sheetName := sheet.attrOr("name", "")
		rID := sheet.attrOr("r:id", "")
		target, ok := relMap[rID]
		if !ok || target == "" {
			continue
		}
		sheetPath := resolveRelativeZipPath("xl", target)
		sheetXML, found, err := pkg.readText(sheetPath)
		if err != nil {
			return Result{}, err
		}
		if !found || sheetXML == "" {
			continue
		}
		wsRoot, err := parseXML(ctx, sheetXML)
		if err != nil {
			return Result{}, fmt.Errorf("Invalid XLSX sheet %s: %w", sheetPath, err)
		}
		sheetData := wsRoot.firstChild("sheetData")
		rows := sheetData.childElements("row")
		if len(rows) == 0 {
			continue
		}

		var tableRows [][]string
		for _, row := range rows {
			var values []string
			for _, cell := range row.childElements("c") {
				values = append(values, extractXlsxCellValue(cell, shared))
			}
			tableRows = append(tableRows, values)
		}
		if len(tableRows) == 0 {
			continue
		}
		tbl := formatMarkdownTable(tableRows)
		if tbl == "" {
			continue
		}
		sections = append(sections, "## "+sheetName)
		sections = append(sections, tbl)
	}

	return Result{Markdown: strings.Join(sections, "\n\n")}, nil
}

func extractXlsxStringItem(si *xmlNode) string {
	if si == nil {
		return ""
	}
	if t := si.firstChild("t"); t != nil {
		return t.textContent()
	}
	runs := si.childElements("r")
	if len(runs) > 0 {
		var b strings.Builder
		for _, r := range runs {
			if t := r.firstChild("t"); t != nil {
				b.WriteString(t.textContent())
			}
		}
		return b.String()
	}
	return ""
}

// extractXlsxCellValue returns the verbatim string value of an XLSX cell.
// upstream: packages/coding-agent/src/markit/converters/xlsx.ts:131-155
func extractXlsxCellValue(cell *xmlNode, shared []string) string {
	cellType := cell.attrOr("t", "")
	switch cellType {
	case "s":
		v := cell.firstChild("v")
		if v == nil {
			return ""
		}
		idx, err := strconv.Atoi(strings.TrimSpace(v.textContent()))
		if err != nil || idx < 0 || idx >= len(shared) {
			return ""
		}
		return shared[idx]
	case "inlineStr":
		return extractXlsxStringItem(cell.firstChild("is"))
	case "b":
		v := cell.firstChild("v")
		if v != nil && strings.TrimSpace(v.textContent()) == "1" {
			return "TRUE"
		}
		return "FALSE"
	default:
		if v := cell.firstChild("v"); v != nil {
			return v.textContent()
		}
		return ""
	}
}
