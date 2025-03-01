package main

import (
	"log"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"
	"encoding/json"

	"github.com/BurntSushi/toml"
	"github.com/PuerkitoBio/goquery"
	"github.com/go-chi/chi"
	"github.com/go-chi/cors"
	"github.com/go-redis/redis"
)

// Server struct for ease of access
type server struct {
	Router *chi.Mux
	DB     *redis.Client
}

// Result returned to client
type resRet struct {
	Html string `json:"html"`
}

// Configs imported from config.toml
type conf struct {
	Port           string
	Username       string
	AllowedOrigins []string
	Expire         int32
	AllowedUsers   string
}

var config = conf{
	Port:           "7800",
	Username:       "mn6",
	AllowedOrigins: []string{"*"},
	Expire:         43200,
	AllowedUsers:   "|xaanit|mn6|",
}

var ghURL string
var dbKey string
var colorFills = map[string]int{
	"var(--color-calendar-graph-day-bg)": 0,
	"var(--color-calendar-graph-day-L1-bg)": 1,
	"var(--color-calendar-graph-day-L2-bg)": 2,
	"var(--color-calendar-graph-day-L3-bg)": 3,
	"var(--color-calendar-graph-day-L4-bg)": 4,
}
var months = map[string]string{
	"01": "Jan",
	"02": "Feb",
	"03": "Mar",
	"04": "Apr",
	"05": "May",
	"06": "Jun",
	"07": "Jul",
	"08": "Aug",
	"09": "Sep",
	"10": "Oct",
	"11": "Nov",
	"12": "Dec",
}

const (
	// Div that will be returned to the client for injection on website
	// .commitscrape is eventually closed off once processing is finished
	csRet string = `
	<div aria-hidden="true" class="commitscrape">
	<style>
	.commitscrape-col:last-child .commitscrape-block{margin-right:0 !important;}
  .commitscrape-0{background-color:#161b22;}
  .commitscrape-1{background-color:#0a373e;}
  .commitscrape-2{background-color:#105f6b;}
  .commitscrape-3{background-color:#09798a;}
  .commitscrape-4{background-color:#05afca;}
  .commitscrape-col{display:inline-grid;}
  .commitscrape-block{border-radius: 3px;}
	</style>
	`
)

var h = &http.Client{
	Timeout: time.Second * 10,
}
var srv server

func main() {
	// DB
	srv.DB = redis.NewClient(&redis.Options{
		Addr:     "localhost:6379",
		Password: "", // no password set
		DB:       0,  // use default DB
	})
	_, err := srv.DB.Ping().Result()
	chk(err)
	dbKey = "commitscrape:" + config.Username

	// Configs
	_, err = toml.DecodeFile("config.toml", &config)
	if err != nil {
		log.Println("No /config.toml file found, setting to default...")
	}
	ghURL = buildGithubURL("")

	// Chi router
	srv.Router = chi.NewRouter()

	// CORS
	cors := cors.New(cors.Options{
		AllowedOrigins: config.AllowedOrigins,
		AllowedMethods: []string{"GET"},
		AllowedHeaders: []string{"*"},
	})
	srv.Router.Use(cors.Handler)

	// Serve it up
	srv.Router.Get("/", calendar)

	log.Printf("Listening for commitscrape requests on port %s", config.Port)
	http.ListenAndServe(":"+config.Port, srv.Router)
}

func calendar(w http.ResponseWriter, r *http.Request) {
	log.Println("Calendar request!")
	var calendarHTML string
	returnJSON := resRet{}
	w.Header().Set("Content-Type", "application/json")
	// Check for ?columns= query
	columnQuery := r.URL.Query().Get("columns")
	userQuery := r.URL.Query().Get("user")
	var cols int
	if len(columnQuery) > 0 {
		// Convert columns query into int and ensure it's between ( 0 and 52 ]
		var err error
		cols, err = strconv.Atoi(columnQuery)
		if err != nil || cols > 52 || cols <= 0 {
			w.WriteHeader(http.StatusBadRequest)
			w.Write([]byte("{ err: \"columns must be a number between 0 and 52\" }"))
			return
		}
	}

	if len(userQuery) > 1 && !strings.Contains(config.AllowedUsers, "|"+userQuery+"|") {
		log.Println(userQuery)
		log.Println(config.AllowedUsers)
		w.WriteHeader(http.StatusBadRequest)
		w.Write([]byte("{ err: \"unauthorized user\"  }"))
		return
	}

	// Get cached HTML from DB
	getCached(&calendarHTML, userQuery)

	// If not cached, go ahead and scrape
	if len(calendarHTML) < 1 {
		scrapeCalendar(&calendarHTML, buildGithubURL(userQuery), userQuery)
	}

	// Parse columns if necessary
	if cols > 0 && len(calendarHTML) > 1 {
		parseColumns(cols, &calendarHTML)
	}

	// Send it over!
	returnJSON.Html = calendarHTML
	js, err := json.Marshal(returnJSON)
	chk(err)

	w.Write(js)
}

// Parse calendar HTML into columns requested
func parseColumns(cols int, calendarHTML *string) {
	rdr := strings.NewReader(*calendarHTML)
	doc, err := goquery.NewDocumentFromReader(rdr)
	chk(err)

	applied, err := doc.Find("body").Find(".commitscrape-col").Slice(0, 53-cols).Remove().End().End().Html()
	chk(err)

	*calendarHTML = applied
}

// Scrape calendar from GH
func scrapeCalendar(calendarHTML *string, url string, userQuery string) {
	retHTML := csRet
	res, err := h.Get(url)
	chk(err)
	defer res.Body.Close()
	if res.StatusCode != 200 {
		return
	}

	doc, err := goquery.NewDocumentFromReader(res.Body)
	chk(err)

	var cols = make(map[int]string)
	doc.Find("div.js-calendar-graph svg rect").Each(func(i int, s *goquery.Selection) {
		index := int(math.Floor(float64(i / 7)))
		fill, _ := s.Attr("data-level")
    fillInt, _ := strconv.Atoi(fill)
		count, _ := s.Attr("data-count")
		date, _ := s.Attr("data-date")
		ret := spawnBlock(fillInt, count, date)
		if len(cols[index]) < 1 {
			cols[index] = ret
		} else {
			cols[index] += ret
		}
	})

	retHTML = retHTML + "<div class=\"commitscrape-col\">" + keysString(cols) + "</div></div>"

	*calendarHTML = retHTML
	saveCal(retHTML, userQuery)
}

// Spawn a square
func spawnBlock(lvl int, count string, date string) string {
	// Craft contribution message
	countMsg := "No contributions"
	if count == "1" {
		countMsg = "1 contribution"
	} else if count != "0" {
		countMsg = count + " contributions"
	}

	// Parse date into human readable format
	parsedDate := parseDate(date)

  if countMsg == " contributions" {
    return ""
  }
	// Return block
	return "<div class=\"commitscrape-block commitscrape-" + strconv.Itoa(lvl) + "\" style=\"width:10px;height:10px;margin-bottom:3px;margin-right:3px;\" data-count=\"" + count + "\" data-date=\"" + date + "\" aria-label=\"" + countMsg + " on " + parsedDate + "\"></div>"
}

// Parse yyyy-mm-dd into mmm dd yyyy
func parseDate(date string) string {
	var end string
	var month string
	splits := strings.Split(date, "-")
	for i, e := range splits {
		// Year
		if i == 0 {
			end = " " + e
		}
		// Month
		if i == 1 {
			month = months[e]
		}
		// Day
		if i == 2 {
			end = e + end
		}
	}

	return month + " " + end
}

// Join map
func keysString(m map[int]string) string {
	keys := []string{}
	for i := 0; i < len(m); i++ {
		keys = append(keys, m[i])
	}
	return strings.Join(keys, "</div><div class=\"commitscrape-col\">")
}

// Save calendar HTML to DB and set expiry to config-provided expire time
func saveCal(calendarHTML string, user string) {
	key := dbKey
	if len(user) > 1 {
		key = "commitscrape:" + user
	}
	srv.DB.Set(key, calendarHTML, time.Duration(config.Expire)*time.Second)
}

// Attempt to get calendar HTML from DB
func getCached(calendarHTML *string, user string) {
	key := dbKey
	if len(user) > 1 {
		key = "commitscrape:" + user
	}
	html, err := srv.DB.Get(key).Result()

	if err == redis.Nil {
		return
	}
	*calendarHTML = html
}

// Build the GitHub URL needed for scrapery
func buildGithubURL(user string) string {
	s := "https://github.com/users/"
	if len(user) < 1 {
		s += config.Username
	} else {
		s += user
	}
	s += "/contributions"
	return s
}

func chk(err error) {
	if err != nil {
		panic(err)
	}
}
