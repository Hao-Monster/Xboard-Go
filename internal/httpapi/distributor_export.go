package httpapi

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/Hao-Monster/Xboard-Go/internal/store"
)

const distributorXLSXContentType = "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet"

var adminDistributorExportHeaders = []string{
	"订单号", "订单类型", "关联原订单", "用户名称", "分销商", "套餐", "周期", "原价", "结算状态", "备注",
}

var userDistributorExportHeaders = []string{
	"订单号", "订单类型", "关联原订单", "用户名称", "订阅计划", "周期", "订单金额", "结算状态", "备注",
}

func (s *server) exportAdminDistributorOrders(w http.ResponseWriter, r *http.Request) {
	filter, ok := distributorExportFilter(w, r, nil, true)
	if !ok {
		return
	}
	s.streamDistributorOrdersExport(w, r, filter, true, "distributor-orders")
}

func (s *server) exportDistributorOrders(w http.ResponseWriter, r *http.Request) {
	session, _ := sessionFromContext(r.Context())
	filter, ok := distributorExportFilter(w, r, &session.UserID, false)
	if !ok {
		return
	}
	s.streamDistributorOrdersExport(w, r, filter, false, "my-distributor-orders")
}

func distributorExportFilter(w http.ResponseWriter, r *http.Request, ownerID *int64, includeTokenSearch bool) (store.DistributorOrderFilter, bool) {
	filter := store.DistributorOrderFilter{
		Page: 1, PageSize: 1, DistributorUserID: ownerID, Search: r.URL.Query().Get("search"), IncludeTokenSearch: includeTokenSearch,
	}
	if raw := strings.TrimSpace(r.URL.Query().Get("distributor_user_id")); ownerID == nil && raw != "" {
		value, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || value < 1 {
			writeAPIError(w, http.StatusUnprocessableEntity, "validation_failed", "distributor_user_id 必须是正整数", nil)
			return store.DistributorOrderFilter{}, false
		}
		filter.DistributorUserID = &value
	}
	if raw := strings.TrimSpace(r.URL.Query().Get("settlement_status")); raw != "" {
		value, err := strconv.Atoi(raw)
		if err != nil || value < 0 || value > 1 {
			writeAPIError(w, http.StatusUnprocessableEntity, "validation_failed", "settlement_status 格式无效", nil)
			return store.DistributorOrderFilter{}, false
		}
		status := store.DistributorSettlementStatus(value)
		filter.SettlementStatus = &status
	}
	return filter, true
}

func (s *server) streamDistributorOrdersExport(w http.ResponseWriter, r *http.Request, filter store.DistributorOrderFilter, admin bool, filenamePrefix string) {
	page, err := s.store.ListDistributorOrders(r.Context(), filter, s.now())
	if err != nil {
		handleStoreError(w, err)
		return
	}
	if page.Total == 0 {
		writeAPIError(w, http.StatusUnprocessableEntity, "no_export_data", "当前筛选条件下没有可导出的订单", nil)
		return
	}
	w.Header().Set("Content-Type", distributorXLSXContentType)
	w.Header().Set("Cache-Control", "no-store, no-cache, must-revalidate")
	filename := fmt.Sprintf("%s-%s.xlsx", filenamePrefix, s.now().UTC().Format("20060102-150405"))
	w.Header().Set("Content-Disposition", `attachment; filename="`+filename+`"`)
	if err := writeDistributorOrdersXLSX(r.Context(), w, s.store, filter, admin); err != nil {
		s.logger.Error("stream distributor order export", "error", err)
	}
}

func writeDistributorOrdersXLSX(ctx context.Context, output io.Writer, database *store.Store, filter store.DistributorOrderFilter, admin bool) error {
	archive := zip.NewWriter(output)
	writeStatic := func(name, content string) error {
		entry, err := archive.Create(name)
		if err != nil {
			return err
		}
		_, err = io.WriteString(entry, content)
		return err
	}
	staticFiles := []struct{ name, content string }{
		{"[Content_Types].xml", xlsxContentTypes},
		{"_rels/.rels", xlsxRootRelationships},
		{"xl/workbook.xml", xlsxWorkbook},
		{"xl/_rels/workbook.xml.rels", xlsxWorkbookRelationships},
		{"xl/styles.xml", xlsxStyles},
	}
	for _, file := range staticFiles {
		if err := writeStatic(file.name, file.content); err != nil {
			_ = archive.Close()
			return fmt.Errorf("write XLSX %s: %w", file.name, err)
		}
	}
	sheet, err := archive.CreateHeader(&zip.FileHeader{Name: "xl/worksheets/sheet1.xml", Method: zip.Deflate})
	if err != nil {
		_ = archive.Close()
		return fmt.Errorf("create XLSX worksheet: %w", err)
	}
	writer := &xlsxStreamWriter{writer: sheet}
	writer.write(`<?xml version="1.0" encoding="UTF-8" standalone="yes"?><worksheet xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main"><sheetViews><sheetView workbookViewId="0"><pane ySplit="1" topLeftCell="A2" activePane="bottomLeft" state="frozen"/></sheetView></sheetViews><cols>`)
	widths := []int{28, 14, 28, 22, 22, 22, 14, 14, 14, 42}
	if !admin {
		widths = []int{28, 14, 28, 22, 22, 14, 14, 14, 42}
	}
	for index, width := range widths {
		writer.write(`<col min="%d" max="%d" width="%d" customWidth="1"/>`, index+1, index+1, width)
	}
	writer.write(`</cols><sheetData>`)
	headers := adminDistributorExportHeaders
	if !admin {
		headers = userDistributorExportHeaders
	}
	writeXLSXRow(writer, 1, headers, -1, 0, true)
	rowNumber := 1
	err = database.StreamDistributorOrderExport(ctx, filter, func(value store.DistributorOrderExportRow) error {
		rowNumber++
		association := "-"
		if value.Type == store.OrderTypeRenewal {
			association = value.SubscriptionTradeNo
		}
		customer := "-"
		if value.CustomerName != nil && strings.TrimSpace(*value.CustomerName) != "" {
			customer = *value.CustomerName
		}
		remark := ""
		if value.Remark != nil {
			remark = *value.Remark
		}
		settlement := "未结算"
		if value.SettlementStatus == store.DistributorSettlementSettled {
			settlement = "已结算"
		}
		values := []string{value.TradeNo, orderTypeLabel(value.Type), association, customer}
		if admin {
			values = append(values, value.DistributorName, value.PlanName, distributorPeriodLabel(value.Period), "", settlement, remark)
			writeXLSXRow(writer, rowNumber, values, 7, value.TotalAmount, false)
		} else {
			values = append(values, value.PlanName, distributorPeriodLabel(value.Period), "", settlement, remark)
			writeXLSXRow(writer, rowNumber, values, 6, value.TotalAmount, false)
		}
		return writer.err
	})
	if err != nil {
		_ = archive.Close()
		return err
	}
	writer.write(`</sheetData><autoFilter ref="A1:%s%d"/></worksheet>`, xlsxColumnName(len(headers)), rowNumber)
	if writer.err != nil {
		_ = archive.Close()
		return fmt.Errorf("write XLSX worksheet: %w", writer.err)
	}
	if err := archive.Close(); err != nil {
		return fmt.Errorf("close XLSX archive: %w", err)
	}
	return nil
}

// writeXLSXRow leaves amountIndex empty so writeAmount can emit a numeric cell.
func writeXLSXRow(writer *xlsxStreamWriter, row int, values []string, amountIndex int, amountCents int64, header bool) {
	writer.write(`<row r="%d">`, row)
	for index, value := range values {
		if index == amountIndex {
			writer.writeAmount(row, index+1, amountCents)
			continue
		}
		style := 0
		if header {
			style = 1
		}
		writer.writeText(row, index+1, value, style)
	}
	writer.write(`</row>`)
}

type xlsxStreamWriter struct {
	writer io.Writer
	err    error
}

func (w *xlsxStreamWriter) write(format string, args ...any) {
	if w.err != nil {
		return
	}
	_, w.err = fmt.Fprintf(w.writer, format, args...)
}

func (w *xlsxStreamWriter) writeText(row, column int, value string, style int) {
	value = safeSpreadsheetText(value)
	w.write(`<c r="%s%d" t="inlineStr" s="%d"><is><t xml:space="preserve">%s</t></is></c>`,
		xlsxColumnName(column), row, style, escapeXMLText(value))
}

func (w *xlsxStreamWriter) writeAmount(row, column int, cents int64) {
	if cents < 0 {
		cents = 0
	}
	w.write(`<c r="%s%d" s="2"><v>%d.%02d</v></c>`, xlsxColumnName(column), row, cents/100, cents%100)
}

func xlsxColumnName(column int) string {
	result := ""
	for column > 0 {
		column--
		result = string(rune('A'+column%26)) + result
		column /= 26
	}
	return result
}

func safeSpreadsheetText(value string) string {
	if !utf8.ValidString(value) {
		value = strings.ToValidUTF8(value, "�")
	}
	value = strings.Map(func(character rune) rune {
		if character < 0x20 && character != '\t' && character != '\n' && character != '\r' {
			return -1
		}
		return character
	}, value)
	trimmed := strings.TrimLeft(value, " \t\r\n")
	if len(trimmed) > 1 && strings.ContainsRune("=+-@", rune(trimmed[0])) {
		return "'" + value
	}
	return value
}

func escapeXMLText(value string) string {
	var output bytes.Buffer
	if err := xml.EscapeText(&output, []byte(value)); err != nil {
		return ""
	}
	return output.String()
}

func distributorPeriodLabel(period string) string {
	labels := map[string]string{
		"monthly": "月付", "quarterly": "季付", "half_yearly": "半年付", "yearly": "年付",
		"two_yearly": "两年付", "three_yearly": "三年付", "onetime": "一次性", "reset_traffic": "重置流量",
	}
	if label := labels[period]; label != "" {
		return label
	}
	return period
}

const xlsxContentTypes = `<?xml version="1.0" encoding="UTF-8" standalone="yes"?><Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types"><Default Extension="rels" ContentType="application/vnd.openxmlformats-package.relationships+xml"/><Default Extension="xml" ContentType="application/xml"/><Override PartName="/xl/workbook.xml" ContentType="application/vnd.openxmlformats-officedocument.spreadsheetml.sheet.main+xml"/><Override PartName="/xl/worksheets/sheet1.xml" ContentType="application/vnd.openxmlformats-officedocument.spreadsheetml.worksheet+xml"/><Override PartName="/xl/styles.xml" ContentType="application/vnd.openxmlformats-officedocument.spreadsheetml.styles+xml"/></Types>`

const xlsxRootRelationships = `<?xml version="1.0" encoding="UTF-8" standalone="yes"?><Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships"><Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/officeDocument" Target="xl/workbook.xml"/></Relationships>`

const xlsxWorkbook = `<?xml version="1.0" encoding="UTF-8" standalone="yes"?><workbook xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main" xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships"><sheets><sheet name="分销订单" sheetId="1" r:id="rId1"/></sheets></workbook>`

const xlsxWorkbookRelationships = `<?xml version="1.0" encoding="UTF-8" standalone="yes"?><Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships"><Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/worksheet" Target="worksheets/sheet1.xml"/><Relationship Id="rId2" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/styles" Target="styles.xml"/></Relationships>`

const xlsxStyles = `<?xml version="1.0" encoding="UTF-8" standalone="yes"?><styleSheet xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main"><numFmts count="1"><numFmt numFmtId="164" formatCode="0.00"/></numFmts><fonts count="2"><font><sz val="11"/><name val="Calibri"/></font><font><b/><color rgb="FFFFFFFF"/><sz val="11"/><name val="Calibri"/></font></fonts><fills count="3"><fill><patternFill patternType="none"/></fill><fill><patternFill patternType="gray125"/></fill><fill><patternFill patternType="solid"><fgColor rgb="FF1F4E78"/><bgColor indexed="64"/></patternFill></fill></fills><borders count="1"><border><left/><right/><top/><bottom/><diagonal/></border></borders><cellStyleXfs count="1"><xf numFmtId="0" fontId="0" fillId="0" borderId="0"/></cellStyleXfs><cellXfs count="3"><xf numFmtId="0" fontId="0" fillId="0" borderId="0" xfId="0"/><xf numFmtId="0" fontId="1" fillId="2" borderId="0" xfId="0" applyFont="1" applyFill="1"/><xf numFmtId="164" fontId="0" fillId="0" borderId="0" xfId="0" applyNumberFormat="1"/></cellXfs><cellStyles count="1"><cellStyle name="Normal" xfId="0" builtinId="0"/></cellStyles></styleSheet>`
