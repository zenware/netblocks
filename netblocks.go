package main

import (
	"encoding/csv"
	"flag"
	"fmt"
	"io"
	"log" // TODO: I need to learn how logging in go works.
	"net/http"
	"net/url"
	"os"
	"path"
	"time"

	"github.com/jlaffaye/ftp"
)

// https://github.com/ioerror/blockfinder/blob/master/block_finder/blockfinder.py
// TODO: Consider using this library for IANA reserved netblocks https://godoc.org/github.com/c-robinson/iplib/iana
// IPLib is the whole library there, and in-fact the entire thing may be useful.

var COUNTRY_CODE_URL = []string{
	"http://www.iso.org/iso/home/standards/country_codes/country_names_and_code_elements_txt-temp.htm",
}

// Download RIR and other stuff
// We Need a GeoIP Database, either one of our own, or supplemented by some external source
func httpDownloadFile(http_uri string, target_filepath string) (err error) {
	// NOTE: From this answer https://stackoverflow.com/a/33853856
	out, err := os.Create(target_filepath)
	if err != nil {
		return err
	}
	// NOTE: Figure out what is defer doing?
	// Best guess is it's forcing it to wait to close the file?
	defer out.Close()

	response, err := http.Get(http_uri)
	if err != nil {
		return err
	}
	defer response.Body.Close()

	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("bad status: %s", response.Status)
	}

	_, err = io.Copy(out, response.Body)
	if err != nil {
		return err
	}

	return nil
}

func ftpDownloadFile(ftp_uri string, target_filepath string) (err error) {
	// NOTE: Library Option found at https://github.com/avelino/awesome-go
	// https://github.com/jlaffaye/ftp
	// Solution from answer https://stackoverflow.com/a/56167966/2025467
	uri, err := url.Parse(ftp_uri)
	if err != nil {
		return err
	}

	connection, err := ftp.Dial(uri.Host, ftp.DialWithTimeout(5*time.Second))
	if err != nil {
		log.Fatal(err)
	}

	connection.ChangeDir(path.Dir(uri.Path))

	response, err := connection.Retr(path.Base(uri.Path))
	if err != nil {
		log.Fatal(err)
	}
	defer response.Close()

	out, err := os.Create(target_filepath)
	if err != nil {
		log.Fatal(err)
	}
	defer out.Close()

	_, err = io.Copy(out, response)
	if err != nil {
		log.Fatal(err)
	}

	if err := connection.Quit(); err != nil {
		log.Fatal(err)
	}

	return nil
}

func downloadMaxmindFiles(data_folder string) (err error) {
	// TODO: Find a new source for this information, this domain seems to be down.
	// TODO: Consider switching this out for a library
	// https://godoc.org/?q=maxmind
	var MAXMIND_URLS = []string{
		"http://geolite.maxmind.com/download/geoip/database/GeoIPCountryCSV.zip",
		"http://geolite.maxmind.com/download/geoip/database/GeoIPv6.csv.gz",
	}

	for _, url := range MAXMIND_URLS {
		target_filepath := data_folder + path.Base(url)
		err := httpDownloadFile(url, target_filepath)
		if err != nil {
			fmt.Printf("%v", err)
			return err
		}
		fmt.Printf("Downloaded %v to %v\n", url, target_filepath)
	}

	return nil
}

// https://en.wikipedia.org/wiki/National_Internet_registry
// IRR - https://www.arin.net/resources/manage/irr/
func downloadRIRFiles(data_folder string) (err error) {
	// Consider adding a LongName
	// Number Resource Organization
	// IANA
	// ICANN
	// https://tools.ietf.org/html/rfc7020
	type RegionalInternetRegistry struct {
		Name        string
		Description string
		URL         string
	}

	type RegionalInternetRegistries []RegionalInternetRegistry

	// TODO: Consider using net/url for these
	var (
		RIRS = RegionalInternetRegistries{
			{"ARIN", "Canada, US, some Caribbean nations", "ftp://ftp.arin.net/pub/stats/arin/delegated-arin-extended-latest"},
			{"RIPE NCC", "Europe, Russia, Middle East, Central Asia", "ftp://ftp.ripe.net/ripe/stats/delegated-ripencc-latest"},
			{"AFRINIC", "Africa", "ftp://ftp.afrinic.net/pub/stats/afrinic/delegated-afrinic-latest"},
			{"APNIC", "Asia-Pacific region", "ftp://ftp.apnic.net/pub/stats/apnic/delegated-apnic-latest"},
			{"LACNIC", "Latin America, some CarribeanNations", "ftp://ftp.lacnic.net/pub/stats/lacnic/delegated-lacnic-latest"},
		}
	)

	for _, rir := range RIRS {
		target_filepath := data_folder + path.Base(rir.URL)
		err := httpDownloadFile(rir.URL, target_filepath)
		if err != nil {
			fmt.Printf("%v\n", err)
			return err
		}
		fmt.Printf("Downloaded %v data from %v to %v\n", rir.Name, rir.URL, target_filepath)
	}

	return nil
}

func downloadLIRFiles(data_folder string) (err error) {
	// an LIR is an organization that has been allocated a block of IP Addresses by an RIR and that assigns most parts of this block to its own customers.
	// Most LIRs are service providers...
	// Why did the original Blockfinder only have two LIR database URLs, we should try to get some more of these.
	var LIR_URLS = []string{
		"ftp://ftp.ripe.net/ripe/dbase/split/ripe.db.inetnum.gz",
		"ftp://ftp.ripe.net/ripe/dbase/split/ripe.db.inet6num.gz",
	}

	for _, url := range LIR_URLS {
		target_filepath := data_folder + path.Base(url)
		err := httpDownloadFile(url, target_filepath)
		if err != nil {
			fmt.Printf("%v\n", err)
			return err
		}
		fmt.Printf("Downloaded %v to %v\n", url, target_filepath)
	}

	return nil
}

func downloadASNAssignments(data_folder string) (err error) {
	// TODO: Download these for some reason
	// var ASN_DESCRIPTION_URL = "http://www.cidr-report.org/as2.0/autnums.html"

	var ASN_ASSIGNMENT_URLS = []string{
		"http://archive.routeviews.org/oix-route-views/oix-full-snapshot-latest.dat.bz2",
	}

	for _, url := range ASN_ASSIGNMENT_URLS {
		target_filepath := data_folder + path.Base(url)
		err := httpDownloadFile(url, target_filepath)
		if err != nil {
			fmt.Printf("%v\n", err)
			return err
		}
		fmt.Printf("Downloaded %v to %v\n", url, target_filepath)
	}

	return nil
}

func initializeDatabases() (err error) {
	fmt.Println("Initializing Databases")
	default_data_folder := "data/"

	if _, err := os.Stat(default_data_folder); os.IsNotExist(err) {
		// NOTE: Numbers can automatically coerce to os.FileMode?
		fmt.Printf("Data folder (%v) does not exist, attempting to create it automatically...\n", default_data_folder)
		err := os.MkdirAll(default_data_folder, 0777)
		if err != nil {
			fmt.Printf("Unable to create data folder (%v)\n", default_data_folder)
			return err
		}
	}

	downloadMaxmindFiles(default_data_folder)
	downloadRIRFiles(default_data_folder)
	downloadLIRFiles(default_data_folder)
	downloadASNAssignments(default_data_folder)
	fmt.Println("Database Initialization Complete")

	return nil
}

func queryByCountryCode(country_code string) string {
	// Use a Sqlite3 or other fast local db
	// Allow a config file ?
	// if country_code.Length() > 2: this is invalid.

	return country_code
}

/*
struct RIRFile {
	header RIRHeader
	delegations []RIRResource

}

struct RIRHeader {
	version_line RIRVersionLine
	summary_lines []RIRSummaryLine
}

struct RIRVersionLine {
	version string 
	registry string 
	serial string 
	records string 
	startdate string 
	enddate string 
	utcoffset string 
}

struct RIRSummaryLine {
	registry string 
	summary_type string 
	count string 
	summary string // Just the text summary
}

struct RIRResource {
	registry string
	cc string 
	resource_type string // could be 'asn', 'ipv4', or 'ipv6
	start string // first IPv4 address of range or AS number or ASN number
	value string // count of hosts for range, doesn't have to represent a CIDR range, count of AS from start value
	date string 
	status string 
	opaque_id string // uniquely identifies an organization (Internet Number Resource Holder)

}
*/

func processRIRDelegations() (err error) {
	//https://www.nro.net/wp-content/uploads/nro-extended-stats-readme5.txt
	// all the "delegated-*" files
	// File Header - version|registry|serial|records|startdate|enddate|UTCoffset
	// records - number of records in the file excluding blank lines, summary lines, the version line, and comments
	// *date - yyyymmdd
	// UTCoffset +/- UTC distance of local RIR producer
	// Summary Line - registry|*|type|*|count|summary
	// Records - registry|cc|type|start|value|date|status|opaque-id[|extensions...]
	// TODO: Check a prebuilt index
	default_data_folder := "data/"
	default_filename := "delegated-arin-extended-latest"

	///open the files
	openfile, err := os.Open(default_data_folder + default_filename)
	checkErr("Error opening file", err)

    // TODO: Trying to use the CSVReader results in the wrong number of fields
    reader := csv.NewReader(openfile)
    reader.Comma = '|'

	filedata, err := reader.ReadAll()
	checkErr("Error reading file", err)

	// read the lines of the file
	for i, value := range filedata {
		// turn the lines of the file into a better format
		fmt.Printf("i = %d(%T), value %s(%T)\n", i, i, value, value)
	}
	return
}

func checkErr(msg string, err error) {
	if err != nil {
		log.Fatal(msg, err)
	}
}


func main() {
	// TODO: Learn exactly what is init() in Go and why I do or don't need it.
	// I'd like a verbosity flag
	// I need a cache dir with a sane default
	// I need a User-Agent for fetching delegation files? FTP UA?
	// IPv4 or IPv6
	// country code
	// Investigate what the --compare option did in blockfinder
	// There should be some Initialization strategy....
	// Should "Config" and "Flags" interlace with each other? i.e. load config first?
	// TODO: First Two Flags... --init, -cc
	// --init downloads all the stuff
	// --cc actually does a query.

	// TODO: Convert flag variables into some kind of "config struct"
	var country_code string
	var initialize_databases bool
	var list_cc bool
	var reserved_count bool

	flag.BoolVar(&initialize_databases, "init", false, "Use this flag to initialize the databases")
	flag.StringVar(&country_code, "cc", "", "Specify the country code to look up")
	flag.BoolVar(&list_cc, "list-cc", false, "Print a list of valid country codes")
	flag.BoolVar(&reserved_count, "reserved-count", false, "Print a count of ARIN Reservations")
	flag.Parse()

	// --list-cc
	// --reserved-count
	if list_cc {
		// TODO: Check a prebuilt index
		//rir_data := processRIRDelegations()
		processRIRDelegations()
		//country_codes := []string
		/*
		fmt.Printf("Country Code List: \n")
		for country_code := range rir_data.country_codes {
			fmt.Printf("\t %v", country_code)

		}
		*/
		return
	}

	if reserved_count {
		processRIRDelegations()
		return
	}

	// We would like a maxmind database, an rir database, an lir database, and a country code database
	// Check that we have these things and if we don't then get them
	if initialize_databases {
		initializeDatabases()
		return
	}

	// If we're not initializing the databases, the only other choice is a query.
	if country_code == "" {
		fmt.Printf("Either use the --init flag or set a 2 character country code with the -cc flag.\n")
		return
	}

	fmt.Printf("Country Code = %v\n", queryByCountryCode(country_code))
}
