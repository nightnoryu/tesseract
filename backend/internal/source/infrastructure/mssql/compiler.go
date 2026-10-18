package mssql

import (
	"database/sql"
	"fmt"
	"strings"

	semantic "github.com/nightnoryu/tesseract/internal/semantic/domain"
	"github.com/nightnoryu/tesseract/internal/source/domain"
)

// compile only accepts an opaque catalog/policy-validated plan. SQL never
// crosses the application or transport boundary.
func (c *Connector) compile(q domain.ValidatedQuery) (string, []any, []string, error) {
	d, ok := c.datasets[q.Dataset()]
	if !ok || q.CatalogHash() == "" || q.CatalogHash() != c.hash {
		return "", nil, nil, domain.ErrInvalidQuery
	}
	spec := q.Spec()
	if spec.Limit < 1 || spec.Limit > c.cfg.MaxRows {
		return "", nil, nil, domain.ErrLimit
	}
	cols := map[string]semantic.Column{}
	dims := map[string]semantic.Dimension{}
	measures := map[string]semantic.Measure{}
	for _, v := range d.Columns {
		cols[v.ID] = v
	}
	for _, v := range d.Dimensions {
		dims[v.ID] = v
	}
	for _, v := range d.Measures {
		measures[v.ID] = v
	}
	args := []any{sql.Named("limit", spec.Limit)}
	bind := func(v domain.Value) string {
		value, _ := v.Parameter()
		name := fmt.Sprintf("p%d", len(args))
		args = append(args, sql.Named(name, value))
		expression := "@" + name
		// SQL Server otherwise applies implicit numeric conversion to string values.
		if v.Type == "decimal" {
			expression = "CAST(" + expression + " AS decimal(38,18))"
		}
		if v.Type == "date" {
			expression = "CAST(" + expression + " AS date)"
		}
		if v.Type == "datetime" {
			expression = "CAST(" + expression + " AS datetime2)"
		}
		return expression
	}
	selects, groups, names := []string{}, []string{}, []string{}
	for _, dim := range spec.Dimensions {
		expression := quote(cols[dims[dim.ID].Column].SourceColumn)
		switch dim.Grain {
		case "day":
			expression = "CAST(" + expression + " AS date)"
		case "week":
			expression = "DATEADD(day, -((DATEDIFF(day, CONVERT(date, '19000101', 112), " + expression + ") % 7 + 7) % 7), CAST(" + expression + " AS date))"
		case "month":
			expression = "DATEFROMPARTS(YEAR(" + expression + "), MONTH(" + expression + "), 1)"
		case "quarter":
			expression = "DATEFROMPARTS(YEAR(" + expression + "), ((MONTH(" + expression + ") - 1) / 3) * 3 + 1, 1)"
		case "year":
			expression = "DATEFROMPARTS(YEAR(" + expression + "), 1, 1)"
		}
		groups = append(groups, expression)
		selects = append(selects, expression+" AS "+quote(dim.ID))
		names = append(names, dim.ID)
	}
	for _, id := range spec.Metrics {
		m := measures[id]
		col := cols[m.Column]
		operand := quote(col.SourceColumn)
		if col.Type == "integer" && (m.Operation == "sum" || m.Operation == "avg") {
			operand = "CAST(" + operand + " AS decimal(38,6))"
		}
		expression := ""
		switch m.Operation {
		case "count_distinct":
			expression = "COUNT_BIG(DISTINCT " + operand + ")"
		case "count":
			expression = "COUNT_BIG(" + operand + ")"
		case "sum", "avg", "min", "max":
			expression = strings.ToUpper(m.Operation) + "(" + operand + ")"
		default:
			return "", nil, nil, domain.ErrInvalidQuery
		}
		selects = append(selects, expression+" AS "+quote(id))
		names = append(names, id)
	}
	predicates := []string{}
	for _, f := range spec.Filters {
		col := quote(cols[dims[f.Dimension].Column].SourceColumn)
		values := []string{}
		for _, v := range f.Values {
			values = append(values, bind(v))
		}
		switch f.Operator {
		case "is_null":
			predicates = append(predicates, col+" IS NULL")
		case "is_not_null":
			predicates = append(predicates, col+" IS NOT NULL")
		case "in", "not_in":
			op := " IN "
			if f.Operator == "not_in" {
				op = " NOT IN "
			}
			predicates = append(predicates, col+op+"("+strings.Join(values, ", ")+")")
		case "between":
			predicates = append(predicates, col+" BETWEEN "+values[0]+" AND "+values[1])
		default:
			op := map[string]string{"eq": "=", "ne": "<>", "lt": "<", "lte": "<=", "gt": ">", "gte": ">="}[f.Operator]
			predicates = append(predicates, col+" "+op+" "+values[0])
		}
	}
	if r := spec.DateRange; r != nil {
		col := quote(cols[dims[r.Dimension].Column].SourceColumn)
		predicates = append(predicates, col+" >= "+bind(r.Start), col+" < "+bind(r.End))
	}
	statement := "SELECT TOP (@limit) " + strings.Join(selects, ", ") + " FROM " + quote(d.Schema) + "." + quote(d.Table)
	if len(predicates) > 0 {
		statement += " WHERE " + strings.Join(predicates, " AND ")
	}
	if len(groups) > 0 {
		statement += " GROUP BY " + strings.Join(groups, ", ")
	}
	order := []string{}
	ordered := map[string]bool{}
	for _, o := range spec.Order {
		order = append(order, quote(o.Field)+" "+strings.ToUpper(o.Direction))
		ordered[o.Field] = true
	}
	// Group keys resolve metric ties and make limited results repeatable.
	for _, d := range spec.Dimensions {
		if !ordered[d.ID] {
			order = append(order, quote(d.ID)+" ASC")
		}
	}
	if len(order) > 0 {
		statement += " ORDER BY " + strings.Join(order, ", ")
	}
	return statement, args, names, nil
}
