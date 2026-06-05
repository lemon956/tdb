package sqlutil

import (
	"fmt"
	"net/url"
	"strconv"
	"strings"

	"tdb/internal/config"
)

func BuildMySQLDSN(profile config.Profile) string {
	return buildDSN(profile, 3306)
}

func BuildDorisDSN(profile config.Profile) string {
	return buildDSN(profile, 9030)
}

func buildDSN(profile config.Profile, defaultPort int) string {
	host := profile.Host
	if host == "" {
		host = "127.0.0.1"
	}
	port := profile.Port
	if port == 0 {
		port = defaultPort
	}
	params, _ := url.ParseQuery(profile.URIParams)
	if params.Get("parseTime") == "" {
		params.Set("parseTime", "true")
	}
	database := strings.TrimPrefix(profile.Database, "/")
	query := params.Encode()
	dsn := fmt.Sprintf("%s:%s@tcp(%s:%d)/%s", profile.User, profile.Password, host, port, database)
	if query != "" {
		dsn += "?" + query
	}
	return dsn
}

func QuoteIdentifier(identifier string) string {
	return "`" + strings.ReplaceAll(identifier, "`", "``") + "`"
}

func LimitOffsetClause(limit, offset int) string {
	pageLimit := limit
	if pageLimit <= 0 {
		pageLimit = 100
	}
	if pageLimit > 500 {
		pageLimit = 500
	}
	if offset < 0 {
		offset = 0
	}
	return " LIMIT " + strconv.Itoa(pageLimit) + " OFFSET " + strconv.Itoa(offset)
}
