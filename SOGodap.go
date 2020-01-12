/*
Copyright 2015-20 Sigma Consulting Services Limited

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

     http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package main

import (
	"database/sql"
	"flag"
	"io/ioutil"
	"log"
	"log/syslog"
	"net"
	"net/url"
	"os"
	"os/signal"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"github.com/DHowett/go-plist"
	"github.com/emersion/go-vcard"
	_ "github.com/go-sql-driver/mysql"
	"github.com/nmcclain/ldap"
)

var conf map[string]interface{}
var db *sql.DB
var dbCombined bool
var debugMode bool
var dsnInfo string
var maxEntries int
var sogo map[string]interface{}
var sogoConf string
var sortDepth int

type Contact struct {
	attr []string
}
 
type Contacts []Contact
 
func (slice Contacts) Len() int {
	return len(slice)
}
 
func (slice Contacts) Less(i, j int) bool {
	var less bool = false
	if sortDepth > len(slice[i].attr) { sortDepth = len(slice[i].attr) }

	for n := 0; n < sortDepth; n++ {
		if slice[i].attr[n] < slice[j].attr[n] {
			less = true
			break
		} else if slice[i].attr[n] > slice[j].attr[n] {
			break
		}
	}

	return less
}
 
func (slice Contacts) Swap(i, j int) {
	slice[i], slice[j] = slice[j], slice[i]
}


func main() {
	// declare command line parameters
	confPtr := flag.String("conf", "/etc/sogo/sogodap.conf", "Location of configuration file")
	debugPtr := flag.Bool("D", false, "Enable debugging output")
	syslogPtr := flag.Bool("syslog", false, "Write output to syslog (instead of stdout)")
	flag.Parse()

	// write to syslog, if requested
	if *syslogPtr {
		logwriter, e := syslog.New(syslog.LOG_NOTICE, "SOGodap")
		if e == nil {
			log.SetOutput(logwriter)
			log.SetFlags(0)
		}
	}

	// output version information
	log.Print("SOGodap v1.1.0 © 2015-20 Sigma Consulting Services")
	debugMode = *debugPtr

	// read SOGodap configuration file
	confFile, err := os.Open(*confPtr)
	if err != nil {
	        log.Fatal(err)
	}
	defer confFile.Close()

	byteConf, _ := ioutil.ReadAll(confFile)
	_, err = plist.Unmarshal([]byte(byteConf), &conf)
	if err != nil {
	        log.Fatal(err)
	}

	// initialise settings from config
	sogoConf = configString(conf, "SogoConf", "/etc/sogo/sogo.conf")
	maxEntries = configInt(conf, "MaxResults", 100)
	sortDepth = configInt(conf, "SortAttributes", 1)

	// read SOGo configuration file path
	debug("Reading database connections from SOGo config file: %s", sogoConf)
	sogoFile, err := os.Open(sogoConf)
	if err != nil {
	        log.Fatal(err)
	}
	defer sogoFile.Close()

	byteSogo, _ := ioutil.ReadAll(sogoFile)
	_, err = plist.Unmarshal([]byte(byteSogo), &sogo)
	if err != nil {
	        log.Fatal(err)
	}

	// extract SOGo database connection string and check table layout
	dsnInfo = getDSN(configString(sogo, "OCSFolderInfoURL"))
	dbCombined = (configString(sogo, "OCSStoreURL") != "")

	// create a database handle
	debug("Connecting to database")
	db, err = sql.Open("mysql", dsnInfo)
	if err != nil {
		log.Fatal("Failed to create database handle: " + err.Error())
	}
	defer db.Close()

	// test database connection
	err = db.Ping()
	if err != nil {
		log.Fatal("Failed to connect to database: " + err.Error())
	}

	// create an LDAP server instance
	s := ldap.NewServer()

	// register Bind and Search function handlers
	handler := ldapHandler{}
	s.BindFunc("", handler)
	s.SearchFunc("", handler)
	s.CloseFunc("", handler)

	// register termination handler
	sigc := make(chan os.Signal, 1)
	signal.Notify(sigc, syscall.SIGINT, syscall.SIGTERM, syscall.SIGQUIT)
	go func() {
		sig := <-sigc
		log.Printf("Beginning shutdown due to %s signal", sig)
		s.Quit <- true
	}()

	// start the server
	listen := configString(conf, "ListenAddress", "127.0.0.1") + ":" + configString(conf, "ListenPort", "10389")
	log.Printf("Starting SOGodap server on %s", listen)
	if err := s.ListenAndServe(listen); err != nil {
		log.Fatal("LDAP Server Failed: " + err.Error())
	}

	// server has quit
	log.Print("SOGodap server shutdown complete")
}


func configInt(list map[string]interface{}, param string, defVal ...int) int {
	var i int = 0

	if len(defVal) > 0 { i = defVal[0] }
	value, found := list[param].(int)
	if found { i = value }

	return i
}


func configString(list map[string]interface{}, param string, defVal ...string) string {
	value, found := list[param].(string)

	if !found {
		if len(defVal) > 0 {
			value = defVal[0]
		} else {
			value = ""
		}
	}

	return value
}


func debug(format string, args ...interface{}) {
	if debugMode {
		log.Printf(format, args...)
	}
}


func findContacts(db *sql.DB, table string, constraint string, attribs []string) Contacts {
	var people Contacts
	query := "select c_content from " + table + " where c_deleted is null" + constraint
	debug("Query for matching contacts: %#v", query)
	contact, err := db.Query(query)
	if err != nil {
		debug("Address book search failed: %s", err.Error())
		return people
	}
	defer contact.Close()

	// loop through returned contacts
	var abEntry string
	for contact.Next() {
		err := contact.Scan(&abEntry)
		if err != nil {
			debug("Failed reading address book entry: %s", err.Error())
			continue
		}

		decoder := vcard.NewDecoder(strings.NewReader(abEntry))
		card, err := decoder.Decode()
		if err == nil {
			person, valid := parseContact(card, attribs)
			if valid { people = append(people, person) }
		} else {
			debug("Failed to read vCard: %s", err)
		}
	}

	return people
}


func findTelephone(phones []*vcard.Field, search string) (string, bool) {
	var number string = ""

	for _, phone := range phones {
		if phone.Params.HasType(search) {
			number = phone.Value
			break
		}
	}

	return number, (number != "")
}

func getDSN(sogoURL string) string {
	var dsn string
	u, err := url.ParseRequestURI(sogoURL)
	if err != nil { debug("Error parsing URL %s: %s", sogoURL, err.Error())	}

	if u.Scheme == "mysql" {
		dsn = u.User.String() + "@tcp(" + u.Host + ")/"
		dsn += strings.Split(u.Path, "/")[1]
	}

	return dsn
}


func getTable(sogoURL string) string {
	return sogoURL[strings.LastIndex(sogoURL, "/") + 1:]
}


func indexOf(ars []string, value string) int {
	for i, s := range ars {
		if strings.ToLower(s) == value {
			return i
		}
	}

	return -1
}


func parseContact(card vcard.Card, attrs []string) (Contact, bool) {
	var person Contact
	var valid bool = false

	if len(attrs) > 0 {
		person.attr = make([]string, len(attrs))
		var value string
		
		for i, attr := range attrs {
			value = ""

			switch strings.ToLower(attr) {
			case "cn":
				value = card.PreferredValue(vcard.FieldFormattedName)
			case "givenname":
				value = card.Name().GivenName
			case "homephone":
				if phone, ok := findTelephone(card[vcard.FieldTelephone], vcard.TypeHome); ok { value = phone }
			case "mobile":
				if phone, ok := findTelephone(card[vcard.FieldTelephone], vcard.TypeCell); ok { value = phone }
			case "o":
				card.PreferredValue(vcard.FieldOrganization)
			case "sn":
				value = card.Name().FamilyName
			case "telephonenumber":
				if phone, ok := findTelephone(card[vcard.FieldTelephone], vcard.TypeWork); ok { value = phone }
			}

			if value != "" {
				person.attr[i] = value
				valid = true
			}
		}
	}

	return person, valid
}

func sqlConstraint(req ldap.SearchRequest, id string) string {
	var sql string

	conjunction := ""
	elements := strings.Split(req.Filter, "(")
	for _, element := range elements {
		switch element {
		case "&":
			conjunction = " and "
		case "|":
			conjunction = " or "
		default:
			if !strings.Contains(element, "=") { continue }
			query := strings.TrimRight(element, ")")
			query = strings.TrimSpace(query)
			query = sqlRegex(query)
			if (query != "") {
				if (len(sql) > 0) && (conjunction != "") { sql += conjunction }
				sql += "c_content regexp '" + query + "'"
			}
		}
	}

	if id != "" {
		if len(sql) > 0 {
			sql = "c_folder_id = " + id + " and (" + sql + ")"
		} else {
			sql = "c_folder_id = " + id
		}
	}

	if len(sql) > 0 { sql = " and " + sql }
	if req.SizeLimit > 0 { sql += " limit " + strconv.Itoa(req.SizeLimit) }

	return sql
}


func sqlRegex(query string) string {
	param := ""
	part := strings.Split(strings.ToLower(query), "=")
	if len(part) == 2 {
		part[1] = strings.Replace(part[1], "*", "", -1)
		param = configString(conf, "Filter_" + part[0])
		param = strings.Replace(param, "_val_", part[1], -1)
	}

	return param
}


func sqlInClause(params []string) string {
	var clause string
	
	for _, param := range params {
		if len(clause) > 0 {
			clause += ","
		} else {
			clause = "("
		}

		clause += "'" + strings.TrimSpace(param) + "'"
	}

	if len(clause) > 0 {
		clause += ")"
	}

	return clause
}


type ldapHandler struct {
}


func (h ldapHandler) Bind(bindDN, bindSimplePw string, conn net.Conn) (ldap.LDAPResultCode, error) {
	if (bindDN == configString(conf, "AuthUser")) && (bindSimplePw == configString(conf, "AuthPass")) {
		debug("Bind succeeded for user %s from %s", bindDN, conn.RemoteAddr().String())
		return ldap.LDAPResultSuccess, nil
	}

	debug("Bind failure for user %s from %s", bindDN, conn.RemoteAddr().String())
	return ldap.LDAPResultInvalidCredentials, nil
}


func (h ldapHandler) Search(boundDN string, searchReq ldap.SearchRequest, conn net.Conn) (ldap.ServerSearchResult, error) {
	log.Printf("Handling search for %s (subtree: %t) from %s", searchReq.BaseDN, (searchReq.Scope == ldap.ScopeWholeSubtree), conn.RemoteAddr().String())

	var infoTable string
	infoTable = getTable(configString(sogo, "OCSFolderInfoURL"))
	users := []string{}

	// enumerate users to search
	users = append(users, strings.TrimPrefix(searchReq.BaseDN, "uid="))
	if searchReq.Scope == ldap.ScopeWholeSubtree {
		shared := configString(conf, "SubtreeLookup")
		users = append(users, strings.Split(shared, ",")...)
	}

	if len(users) == 0 {
		debug("No user address books defined to search")
		return ldap.ServerSearchResult{[]*ldap.Entry{}, []string{}, []ldap.Control{}, ldap.LDAPResultNoSuchObject}, nil
	}

	// check and set result limit
	if (searchReq.SizeLimit == 0) || (searchReq.SizeLimit > maxEntries) {
		searchReq.SizeLimit = maxEntries
	}
	debug("Limiting results to %d entries", searchReq.SizeLimit)

	// locate address books to search
	var query string
	debug("Searching table %s for address books of %s", infoTable, users)
	debug("Returning attributes: %s", searchReq.Attributes)
	if dbCombined {
		query = "select c_folder_id from " + infoTable + " where c_folder_type = 'Contact' and c_path2 in " + sqlInClause(users)
	} else {
		query = "select c_location from " + infoTable + " where c_folder_type = 'Contact' and c_path2 in " + sqlInClause(users)
	}
	addrBook, err := db.Query(query)
	if err != nil {
		debug("Address book table lookup failed: %s", err.Error)
		return ldap.ServerSearchResult{[]*ldap.Entry{}, []string{}, []ldap.Control{}, ldap.LDAPResultOperationsError}, nil
	}
	defer addrBook.Close()

	// loop through returned address books
	var abID string
	var people Contacts
	for addrBook.Next() {
		err := addrBook.Scan(&abID)
		if err != nil {
			debug("Failed reading address book info: %s", err.Error())
			continue
		}

		var abDSN string
		var abTable string
		var abConstraint string

		if dbCombined {
			abDSN = getDSN(configString(sogo, "OCSStoreURL"))
			abTable = getTable(configString(sogo, "OCSStoreURL"))
			abConstraint = sqlConstraint(searchReq, abID)
		} else {
			abDSN = getDSN(abID)
			abTable = getTable(abID)
			abConstraint = sqlConstraint(searchReq, "")
		}

		// check if new DB handle is needed (pool if not)
		if abDSN == dsnInfo {
			people = append(people, findContacts(db, abTable, abConstraint, searchReq.Attributes)...)
		} else {
			// create a database handle
			debug("Connecting to database for address book search")
			abDB, err := sql.Open("mysql", abDSN)
			if err != nil {
				debug("Failed creating address book handle: %s", err.Error())
				continue
			}
			defer abDB.Close()

			// test database connection
			err = abDB.Ping()
			if err != nil {
				debug("Failed connecting to address book: %s", err.Error())
				continue
			}

			people = append(people, findContacts(abDB, abTable, abConstraint, searchReq.Attributes)...)
			abDB.Close()
		}
	}

	// sort result set
	if sortDepth > 0 {
		debug("Sorting to a depth of %d attributes", sortDepth)
		sort.Sort(people)
	}

	// ensure maximum result entries are respected
	if len(people) > searchReq.SizeLimit {
		debug("Pruning combined result set from %d to %d entries", len(people), searchReq.SizeLimit)
		people = people[:searchReq.SizeLimit]
	}

	// write contacts as LDAP result entries
	var results []*ldap.Entry
	for i, person := range people {
		var result ldap.Entry
		result.DN = "cn=" + strconv.Itoa(i) + "," + searchReq.BaseDN

		for n, a := range person.attr {
			var entry ldap.EntryAttribute
			entry.Name = searchReq.Attributes[n]
			entry.Values = []string{a}
			result.Attributes = append(result.Attributes, &entry)
		}

		results = append(results, &result)
	}

	debug("Sending %d result entries: %s", len(results), people)
	return ldap.ServerSearchResult{results, []string{}, []ldap.Control{}, ldap.LDAPResultSuccess}, nil
}


func (h ldapHandler) Close(boundDN string, conn net.Conn) error {
	log.Printf("Closing connection from %s", conn.RemoteAddr().String())

	return nil
}
