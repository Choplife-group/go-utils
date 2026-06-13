package library

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"os"
	"strconv"
	"strings"

	"github.com/HeyTwoHeads/go-utils/models"
)

// buildCountQuery returns a COUNT query that correctly handles GROUP BY.
//
// Without GROUP BY:
//
//	SELECT count(id) FROM table JOIN ... WHERE ...
//
// With GROUP BY, wrapping in a subquery counts the number of groups rather
// than the number of raw rows, which is what pagination needs:
//
//	SELECT count(*) FROM (SELECT primaryKey FROM table JOIN ... WHERE ... GROUP BY ...) AS _count_subquery
func buildCountQuery(primaryKey, tableName, joinQuery, whereClause, groupByClause string) string {

	if groupByClause == "" {

		return fmt.Sprintf(
			"SELECT count(%s) as total FROM %s %s WHERE %s ",
			primaryKey, tableName, joinQuery, whereClause,
		)
	}

	// FIX: wrap grouped query in a subquery so we count groups, not rows.
	inner := fmt.Sprintf(
		"SELECT %s FROM %s %s WHERE %s %s",
		primaryKey, tableName, joinQuery, whereClause, groupByClause,
	)

	return fmt.Sprintf("SELECT COUNT(*) AS total FROM (%s) AS _count_subquery", inner)
}

func PaginateDataWithContext(ctx context.Context, db *sql.DB, paginator models.Paginator) models.Pagination {

	search := paginator.VueTable
	joins := paginator.Joins
	fields := paginator.Fields
	orWhere := paginator.OrWhere
	having := paginator.Having
	groupBy := paginator.GroupBy
	params := paginator.Params
	tableName := paginator.TableName
	primaryKey := paginator.PrimaryKey

	isDebug, _ := strconv.ParseInt(os.Getenv("DEBUG"), 10, 64)

	perPage := int(search.PerPage)
	page := int(search.Page)

	joinQuery := strings.Join(joins[:], " ")
	field := strings.Join(fields[:], ",")

	whereQuery := func() string {

		if len(orWhere) > 0 {

			return strings.Join(orWhere[:], " AND ")
		}
		return "1"
	}

	havingQuery := func() string {

		if len(having) > 0 {

			return fmt.Sprintf("HAVING %s", strings.Join(having[:], " AND "))
		}

		return ""
	}

	group := func() string {

		if len(groupBy) > 0 {

			return fmt.Sprintf("GROUP BY %s", strings.Join(groupBy[:], " , "))

		}

		return ""
	}

	// build order by query
	orderBy := ""

	if len(search.Sort) > 0 {

		parts := strings.Split(search.Sort, ",")

		var orders []string

		for _, p := range parts {

			sortPrams := strings.Split(p, "|")

			if len(sortPrams) == 2 {

				column := sortPrams[0]
				direction := sortPrams[1]
				orders = append(orders, fmt.Sprintf("%s %s ", column, direction))
			}

		}

		if len(orders) > 0 {

			orderBy = fmt.Sprintf("ORDER BY %s ", strings.Join(orders, ","))

		}
	}

	// FIX: use buildCountQuery so GROUP BY pagination counts groups, not rows.
	countQuery := buildCountQuery(primaryKey, tableName, joinQuery, whereQuery(), group())

	total := 0

	dbUtil := Db{DB: db, Context: ctx}

	dbUtil.SetQuery(countQuery)
	dbUtil.SetParams(params...)

	if isDebug != 0 {

		log.Printf("Count Query | %s", countQuery)
		log.Printf("Params | %v", params...)

	}

	err := dbUtil.FetchOneWithContext().Scan(&total)
	if err != nil {

		log.Printf("got error retrieving total number of records %s ", err.Error())
		return models.Pagination{}
	}

	// calculate offset
	lastPage := CalculateTotalPages(total, perPage)

	currentPage := page - 1
	offset := 0

	if currentPage > 0 {

		offset = perPage * currentPage

	} else {

		currentPage = 0
		offset = 0
	}

	if offset > total {

		offset = total - (currentPage * perPage)
	}

	from := offset + 1
	currentPage++

	limit := fmt.Sprintf(" LIMIT %d,%d", offset, perPage)

	sqlQuery := fmt.Sprintf("SELECT %s FROM %s %s WHERE %s %s %s %s %s", field, tableName, joinQuery, whereQuery(), group(), havingQuery(), orderBy, limit)

	if isDebug != 0 {

		log.Printf("Data Query | %s", sqlQuery)

	}

	var resp models.Pagination

	dbUtil.SetQuery(sqlQuery)

	rows, err := dbUtil.FetchWithContext()
	if err != nil {

		log.Printf("error pulling vuetable data %s", err.Error())

		resp.Total = total
		resp.PerPage = perPage
		resp.CurrentPage = currentPage
		resp.LastPage = lastPage
		resp.From = from
		resp.To = 0
		resp.Data = make(map[string]interface{})
		return resp

	}

	defer rows.Close()

	data := paginator.Results(rows)
	resp.Total = total
	resp.PerPage = perPage
	resp.CurrentPage = currentPage
	resp.LastPage = lastPage
	resp.From = from
	resp.To = offset + len(data)
	resp.Data = data

	return resp
}

func DownloadPaginatedDataWithContext(ctx context.Context, db *sql.DB, paginator models.Paginator) (rowData []interface{}, headrs []string) {

	search := paginator.VueTable
	joins := paginator.Joins
	fields := paginator.Fields
	orWhere := paginator.OrWhere
	having := paginator.Having
	groupBy := paginator.GroupBy
	params := paginator.Params
	tableName := paginator.TableName
	primaryKey := paginator.PrimaryKey
	isDebug, _ := strconv.ParseInt(os.Getenv("DEBUG"), 10, 64)

	joinQuery := strings.Join(joins[:], " ")
	field := strings.Join(fields[:], ",")

	var headers []string

	for _, h := range fields {

		parts := strings.Split(h, " ")
		headers = append(headers, parts[len(parts)-1])
	}

	whereQuery := func() string {

		if len(orWhere) > 0 {

			return strings.Join(orWhere[:], " AND ")
		}

		return "1"
	}

	havingQuery := func() string {

		if len(having) > 0 {

			return fmt.Sprintf("HAVING %s", strings.Join(having[:], " AND "))
		}

		return ""
	}

	group := func() string {

		if len(groupBy) > 0 {

			return fmt.Sprintf("GROUP BY %s", strings.Join(groupBy[:], " , "))

		}

		return ""
	}

	// build order by query
	orderBy := ""

	if len(search.Sort) > 0 {

		parts := strings.Split(search.Sort, ",")

		var orders []string

		for _, p := range parts {

			sortPrams := strings.Split(p, "|")

			if len(sortPrams) == 2 {

				column := sortPrams[0]
				direction := sortPrams[1]
				orders = append(orders, fmt.Sprintf("%s %s ", column, direction))
			}

		}

		if len(orders) > 0 {

			orderBy = fmt.Sprintf("ORDER BY %s ", strings.Join(orders, ","))

		}
	}

	hardLimit, _ := strconv.ParseInt(os.Getenv("HARD_SQL_FETCH_LIMIT"), 10, 64)
	if hardLimit == 0 {
		hardLimit = 200000
	}

	// FIX: use buildCountQuery so GROUP BY counts groups, not rows.
	// For downloads, the hard limit cap is applied after the subquery where relevant.
	var countQuery string

	if hardLimit == -1 {

		countQuery = buildCountQuery(primaryKey, tableName, joinQuery, whereQuery(), group())

	} else {

		baseCount := buildCountQuery(primaryKey, tableName, joinQuery, whereQuery(), group())
		countQuery = fmt.Sprintf("%s LIMIT %d", baseCount, hardLimit)

	}

	total := 0

	dbUtil := Db{DB: db, Context: ctx}
	dbUtil.SetQuery(countQuery)
	dbUtil.SetParams(params...)

	if isDebug != 0 {

		log.Printf("Count Query | %s", countQuery)
		log.Printf("Params | %v", params...)

	}

	err := dbUtil.FetchOneWithContext().Scan(&total)
	if err != nil {

		log.Printf("got error retrieving total number of records %s ", err.Error())
		return nil, headers
	}

	var sqlQuery string

	if hardLimit == -1 {

		sqlQuery = fmt.Sprintf("SELECT %s FROM %s %s WHERE %s %s %s %s", field, tableName, joinQuery, whereQuery(), group(), havingQuery(), orderBy)

	} else {

		sqlQuery = fmt.Sprintf("SELECT %s FROM %s %s WHERE %s %s %s %s LIMIT %d", field, tableName, joinQuery, whereQuery(), group(), havingQuery(), orderBy, hardLimit)

	}

	dbUtil.SetQuery(sqlQuery)

	if isDebug != 0 {

		log.Printf("Data Query | %s", sqlQuery)

	}

	rows, err := dbUtil.FetchWithContext()
	if err != nil {

		log.Printf("error pulling vuetable data %s", err.Error())
		return nil, headers

	}

	defer rows.Close()

	rowData = paginator.Results(rows)

	return rowData, headers
}

func PaginateDataSlaveWithContext(ctx context.Context, dbSlave *sql.DB, paginator models.Paginator) models.Pagination {

	search := paginator.VueTable
	joins := paginator.Joins
	fields := paginator.Fields
	orWhere := paginator.OrWhere
	having := paginator.Having
	groupBy := paginator.GroupBy
	params := paginator.Params
	tableName := paginator.TableName
	primaryKey := paginator.PrimaryKey

	isDebug, _ := strconv.ParseInt(os.Getenv("DEBUG"), 10, 64)

	perPage := int(search.PerPage)
	page := int(search.Page)

	joinQuery := strings.Join(joins[:], " ")
	field := strings.Join(fields[:], ",")

	whereQuery := func() string {

		if len(orWhere) > 0 {

			return strings.Join(orWhere[:], " AND ")
		}

		return "1"
	}

	havingQuery := func() string {

		if len(having) > 0 {

			return fmt.Sprintf("HAVING %s", strings.Join(having[:], " AND "))
		}

		return ""
	}

	group := func() string {

		if len(groupBy) > 0 {

			return fmt.Sprintf("GROUP BY %s", strings.Join(groupBy[:], " , "))

		}

		return ""
	}

	// build order by query
	orderBy := ""

	if len(search.Sort) > 0 {

		parts := strings.Split(search.Sort, ",")

		var orders []string

		for _, p := range parts {

			sortPrams := strings.Split(p, "|")

			if len(sortPrams) == 2 {

				column := sortPrams[0]
				direction := sortPrams[1]
				orders = append(orders, fmt.Sprintf("%s %s ", column, direction))
			}

		}

		if len(orders) > 0 {

			orderBy = fmt.Sprintf("ORDER BY %s ", strings.Join(orders, ","))

		}
	}

	// FIX: use buildCountQuery so GROUP BY pagination counts groups, not rows.
	countQuery := buildCountQuery(primaryKey, tableName, joinQuery, whereQuery(), group())

	total := 0

	dbUtil := Db{DBSlave: dbSlave, Context: ctx}
	dbUtil.SetQuery(countQuery)
	dbUtil.SetParams(params...)

	if isDebug != 0 {

		log.Printf("Count Query | %s", countQuery)
		log.Printf("Params | %v", params...)

	}

	err := dbUtil.FetchOneSlaveWithContext().Scan(&total)
	if err != nil {

		log.Printf("got error retrieving total number of records %s ", err.Error())

		return models.Pagination{}
	}

	// calculate offset
	lastPage := CalculateTotalPages(total, perPage)

	currentPage := page - 1
	offset := 0

	if currentPage > 0 {

		offset = perPage * currentPage

	} else {

		currentPage = 0
		offset = 0
	}

	if offset > total {

		offset = total - (currentPage * perPage)
	}

	from := offset + 1
	currentPage++

	limit := fmt.Sprintf(" LIMIT %d,%d", offset, perPage)

	sqlQuery := fmt.Sprintf("SELECT %s FROM %s %s WHERE %s %s %s %s %s", field, tableName, joinQuery, whereQuery(), group(), havingQuery(), orderBy, limit)

	if isDebug != 0 {

		log.Printf("Data Query | %s", sqlQuery)

	}

	var resp models.Pagination

	dbUtil.SetQuery(sqlQuery)

	rows, err := dbUtil.FetchSlaveWithContext()
	if err != nil {

		log.Printf("error pulling vuetable data %s", err.Error())

		resp.Total = total
		resp.PerPage = perPage
		resp.CurrentPage = currentPage
		resp.LastPage = lastPage
		resp.From = from
		resp.To = 0
		resp.Data = make(map[string]interface{})

		return resp
	}

	defer rows.Close()

	data := paginator.Results(rows)
	resp.Total = total
	resp.PerPage = perPage
	resp.CurrentPage = currentPage
	resp.LastPage = lastPage
	resp.From = from
	resp.To = offset + len(data)
	resp.Data = data

	return resp
}

func DownloadPaginatedDataSlaveWithContext(ctx context.Context, dbSlave *sql.DB, paginator models.Paginator) (rowData []interface{}, headrs []string) {

	search := paginator.VueTable
	joins := paginator.Joins
	fields := paginator.Fields
	orWhere := paginator.OrWhere
	having := paginator.Having
	groupBy := paginator.GroupBy
	params := paginator.Params
	tableName := paginator.TableName
	primaryKey := paginator.PrimaryKey
	isDebug, _ := strconv.ParseInt(os.Getenv("DEBUG"), 10, 64)

	joinQuery := strings.Join(joins[:], " ")
	field := strings.Join(fields[:], ",")

	var headers []string

	for _, h := range fields {

		parts := strings.Split(h, " ")
		headers = append(headers, parts[len(parts)-1])
	}

	whereQuery := func() string {

		if len(orWhere) > 0 {

			return strings.Join(orWhere[:], " AND ")
		}

		return "1"
	}

	havingQuery := func() string {

		if len(having) > 0 {

			return fmt.Sprintf("HAVING %s", strings.Join(having[:], " AND "))
		}

		return ""
	}

	group := func() string {

		if len(groupBy) > 0 {

			return fmt.Sprintf("GROUP BY %s", strings.Join(groupBy[:], " , "))

		}

		return ""
	}

	// build order by query
	orderBy := ""

	if len(search.Sort) > 0 {

		parts := strings.Split(search.Sort, ",")

		var orders []string

		for _, p := range parts {

			sortPrams := strings.Split(p, "|")

			if len(sortPrams) == 2 {

				column := sortPrams[0]
				direction := sortPrams[1]
				orders = append(orders, fmt.Sprintf("%s %s ", column, direction))
			}

		}

		if len(orders) > 0 {

			orderBy = fmt.Sprintf("ORDER BY %s ", strings.Join(orders, ","))

		}
	}

	hardLimit, _ := strconv.ParseInt(os.Getenv("HARD_SQL_FETCH_LIMIT"), 10, 64)
	if hardLimit == 0 {

		hardLimit = 200000
	}

	// FIX: use buildCountQuery so GROUP BY counts groups, not rows.
	var countQuery string

	if hardLimit == -1 {

		countQuery = buildCountQuery(primaryKey, tableName, joinQuery, whereQuery(), group())

	} else {

		baseCount := buildCountQuery(primaryKey, tableName, joinQuery, whereQuery(), group())
		countQuery = fmt.Sprintf("%s LIMIT %d", baseCount, hardLimit)

	}

	total := 0

	dbUtil := Db{DBSlave: dbSlave, Context: ctx}

	dbUtil.SetQuery(countQuery)
	dbUtil.SetParams(params...)

	if isDebug != 0 {

		log.Printf("Count Query | %s", countQuery)
		log.Printf("Params | %v", params...)

	}

	err := dbUtil.FetchOneSlaveWithContext().Scan(&total)
	if err != nil {

		log.Printf("got error retrieving total number of records %s ", err.Error())
		return nil, headers
	}

	var sqlQuery string

	if hardLimit == -1 {

		sqlQuery = fmt.Sprintf("SELECT %s FROM %s %s WHERE %s %s %s %s", field, tableName, joinQuery, whereQuery(), group(), havingQuery(), orderBy)

	} else {

		sqlQuery = fmt.Sprintf("SELECT %s FROM %s %s WHERE %s %s %s %s LIMIT %d", field, tableName, joinQuery, whereQuery(), group(), havingQuery(), orderBy, hardLimit)

	}

	dbUtil.SetQuery(sqlQuery)

	if isDebug != 0 {

		log.Printf("Data Query | %s", sqlQuery)

	}

	rows, err := dbUtil.FetchSlaveWithContext()
	if err != nil {

		log.Printf("error pulling vuetable data %s", err.Error())
		return nil, headers

	}

	defer rows.Close()

	rowData = paginator.Results(rows)

	return rowData, headers
}
