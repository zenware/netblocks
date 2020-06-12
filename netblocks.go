package main

import (
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"path"
	"time"

	"github.com/jlaffaye/ftp"
)

// https://github.com/ioerror/blockfinder/blob/master/block_finder/blockfinder.py

var COUNTRY_CODE_URL = []string{
	"http://www.iso.org/iso/home/standards/country_codes/country_names_and_code_elements_txt-temp.htm",
}

// Download RIR and other stuff
// We Need a GeoIP Database, either one of our own, or supplemented by some external source
func HTTPDownloadFile(http_uri string, target_filepath string) (err error) {
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

func FTPDownloadFile(ftp_uri string, target_filepath string) (err error) {
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

func DownloadMaxmindFiles(data_folder string) (err error) {
    // TODO: Find a new source for this information, this domain seems to be down.
	var MAXMIND_URLS = []string{
		"http://geolite.maxmind.com/download/geoip/database/GeoIPCountryCSV.zip",
		"http://geolite.maxmind.com/download/geoip/database/GeoIPv6.csv.gz",
	}

	for _, url := range MAXMIND_URLS {
		target_filepath := data_folder + path.Base(url)
		err := HTTPDownloadFile(url, target_filepath)
		if err != nil {
            fmt.Printf("%v", err)
			return err
		}
		fmt.Printf("Downloaded %v to %v\n", url, target_filepath)
	}

	return nil
}

// https://en.wikipedia.org/wiki/National_Internet_registry
func DownloadRIRFiles(data_folder string) (err error) {
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
		err := HTTPDownloadFile(rir.URL, target_filepath)
		if err != nil {
            fmt.Printf("%v\n", err)
			return err
		}
		fmt.Printf("Downloaded %v data from %v to %v\n", rir.Name, rir.URL, target_filepath)
	}

	return nil
}

func DownloadLIRFiles(data_folder string) (err error) {
	// an LIR is an organization that has been allocated a block of IP Addresses by an RIR and that assigns most parts of this block to its own customers.
	// Most LIRs are service providers...
	// Why did the original Blockfinder only have two LIR database URLs, we should try to get some more of these.
	var LIR_URLS = []string{
		"ftp://ftp.ripe.net/ripe/dbase/split/ripe.db.inetnum.gz",
		"ftp://ftp.ripe.net/ripe/dbase/split/ripe.db.inet6num.gz",
	}

	for _, url := range LIR_URLS {
		target_filepath := data_folder + path.Base(url)
		err := HTTPDownloadFile(url, target_filepath)
		if err != nil {
            fmt.Printf("%v\n", err)
			return err
		}
		fmt.Printf("Downloaded %v to %v\n", url, target_filepath)
	}

	return nil
}

func DownloadASNAssignments(data_folder string) (err error) {
	// TODO: Download these for some reason
	// var ASN_DESCRIPTION_URL = "http://www.cidr-report.org/as2.0/autnums.html"

	var ASN_ASSIGNMENT_URLS = []string{
		"http://archive.routeviews.org/oix-route-views/oix-full-snapshot-latest.dat.bz2",
	}

	for _, url := range ASN_ASSIGNMENT_URLS {
		target_filepath := data_folder + path.Base(url)
		err := HTTPDownloadFile(url, target_filepath)
		if err != nil {
            fmt.Printf("%v\n", err)
			return err
		}
		fmt.Printf("Downloaded %v to %v\n", url, target_filepath)
	}

	return nil
}

func InitializeDatabases() (err error) {
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

	DownloadMaxmindFiles(default_data_folder)
	DownloadRIRFiles(default_data_folder)
	DownloadLIRFiles(default_data_folder)
    DownloadASNAssignments(default_data_folder)
    fmt.Println("Database Initialization Complete")

    return nil
}

func QueryByCountryCode(country_code string) string {
	// Use a Sqlite3 or other fast local db
	// Allow a config file ?

	return country_code
}

var country_code string
var initialize_databases bool

func init() {
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
	flag.BoolVar(&initialize_databases, "init", false, "Use this flag to initialize the databases")
	flag.StringVar(&country_code, "cc", "", "Specify the country code to look up")
}

func main() {
	flag.Parse()

	// We would like a maxmind database, an rir database, an lir database, and a country code database
	// Check that we have these things and if we don't then get them
	if initialize_databases == true {
		InitializeDatabases()
		return
	}

	// If we're not initializing the databases, the only other choice is a query.
	if initialize_databases == false && country_code == "" {
		fmt.Printf("Either use the --init flag or set a 2 character country code with the -cc flag.\n")
		return
	}

	fmt.Printf("Country Code = %v\n", QueryByCountryCode(country_code))
}
