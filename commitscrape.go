package main

import (
	"log"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"

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

// Configs imported from config.toml
type conf struct {
	Port           string
	Username       string
	AllowedOrigins []string
	Expire         int32
}

var config = conf{
	Port:           "7800",
	Username:       "mn6",
	AllowedOrigins: []string{"*"},
	Expire:         43200,
}

var ghURL string
var dbKey string
var colorFills = map[string]int{
	"#ebedf0": 0,
	"#c6e48b": 1,
	"#7bc96f": 2,
	"#196127": 3,
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
	<div aria-hidden="true" class="commitscrape" style="display:flex;flex-direction:row;width:fit-content;">
	<style>
	.commitscrape-col:last-child .commitscrape-block{margin-right:0 !important;}
	.commitscrape-0{background-color:#83c8e6;}
	.commitscrape-1{background-color:#3d96bd;}
	.commitscrape-2{background-color:#166284;}
	.commitscrape-3{background-color:#0b455f;}
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
	ghURL = buildGithubURL()

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
	w.Header().Set("Content-Type", "application/json")
	// Check for ?columns= query
	columnQuery := r.URL.Query().Get("columns")
	var cols int
	if len(columnQuery) > 0 {
		// Convert columns query into int and ensure it's between ( 0 and 52 ]
		var err error
		cols, err = strconv.Atoi(columnQuery)
		if err != nil || cols > 52 || cols <= 0 {
			w.WriteHeader(http.StatusBadRequest)
			w.Write([]byte("{ err: \"columns must be a number between 0 and 52\" }"))
		}
	}

	// Get cached HTML from DB
	getCached(&calendarHTML)

	// If not cached, go ahead and scrape
	if len(calendarHTML) < 1 {
		scrapeCalendar(&calendarHTML)
	}

	// Parse columns if necessary
	if cols > 0 && len(calendarHTML) > 1 {
		parseColumns(cols, &calendarHTML)
	}

	// Send it over!
	w.Write([]byte("{ html: \"" + calendarHTML + "\" }"))
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
func scrapeCalendar(calendarHTML *string) {
	retHTML := csRet
	res, err := h.Get(ghURL)
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
		fill, _ := s.Attr("fill")
		count, _ := s.Attr("data-count")
		date, _ := s.Attr("data-date")
		ret := spawnBlock(colorFills[fill], count, date)
		if len(cols[index]) < 1 {
			cols[index] = ret
		} else {
			cols[index] += ret
		}
	})

	retHTML = retHTML + "<div class=\"commitscrape-col\">" + keysString(cols) + "</div></div>"

	*calendarHTML = retHTML
	saveCal(retHTML)
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
func saveCal(calendarHTML string) {
	srv.DB.Set(dbKey, calendarHTML, time.Duration(config.Expire)*time.Second)
}

// Attempt to get calendar HTML from DB
func getCached(calendarHTML *string) {
	html, err := srv.DB.Get(dbKey).Result()

	if err == redis.Nil {
		return
	}
	*calendarHTML = html
}

// Build the GitHub URL needed for scrapery
func buildGithubURL() string {
	s := "https://github.com/users/"
	s += config.Username
	s += "/contributions"
	return s
}

func chk(err error) {
	if err != nil {
		panic(err)
	}
}
