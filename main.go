package main

import (
	"bufio"
	"flag"
	"fmt"
	"io/ioutil"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/stianeikeland/go-rpio/v4"
	"go.bug.st/serial"
)

type SphincterStatus int

const (
	UNKNOWN  = 0 // no power?
	LOCKED   = 1
	UNLOCKED = 2
	FAILURE  = 3
)

var (
	list = flag.String("list", "list.txt", "account list")
	port = flag.String("port", "/dev/ttyUSB0", "reader device")

	OpenPin    rpio.Pin = rpio.Pin(22) // 15
	ClosePin   rpio.Pin = rpio.Pin(27) // 13
	StatusPin0 rpio.Pin = rpio.Pin(4)  // 7
	StatusPin1 rpio.Pin = rpio.Pin(17) // 11

	sphincterStatus SphincterStatus
	CmdChan         = make(chan string)
	UpdateChan      = make(chan bool)

	latestTimestamp time.Time
)

func getRFIDToken(port *serial.Port) chan string {
	c := make(chan string)

	go func() {
		for {
			rd := bufio.NewReader(*port)
			res, err := rd.ReadBytes('\x03')
			if err != nil {
				// If there was an error while reading from the port,
				// panic so daemon will restart
				panic(err)
			}
			s := strings.Replace(string(res), "\x03", "", -1)
			s = strings.Replace(s, "\x02", "", -1)
			c <- s
		}
	}()

	return c
}

func parseUserList() (map[string]string, error) {
	users := map[string]string{}
	bytes, err := ioutil.ReadFile(*list)
	if err != nil {
		return users, err
	}
	lines := strings.Split(string(bytes), "\n")
	for _, line := range lines {
		fields := strings.Fields(line)
		if len(fields) > 1 {
			users[fields[0]] = strings.Join(fields[1:], " ")
		}
	}

	return users, nil
}

// If token only contains 0 and/or F's, its not a valid token
func isValid(token string) bool {
	token = strings.ReplaceAll(token, "F", "")
	token = strings.ReplaceAll(token, "0", "")
	return len(token) > 0
}

func updateSphincterStatus() SphincterStatus {
	if StatusPin0.Read() == rpio.Low &&
		StatusPin1.Read() == rpio.Low {
		sphincterStatus = UNKNOWN
	}
	if StatusPin0.Read() == rpio.High &&
		StatusPin1.Read() == rpio.High {
		sphincterStatus = FAILURE
	}
	if StatusPin0.Read() == rpio.High &&
		StatusPin1.Read() == rpio.Low {
		sphincterStatus = UNLOCKED
	}
	if StatusPin0.Read() == rpio.Low &&
		StatusPin1.Read() == rpio.High {
		sphincterStatus = LOCKED
	}
	return sphincterStatus
}

func sphincterOpen() bool {
	return false
}
func sphincterClose() bool {
	return false
}

func setupSphincterCmdChannel() {
	go func() {
		for cmd := range CmdChan {
			log.Println("cmd: %s", cmd)
			switch {
			case cmd == "open":
				OpenPin.High()
				// TODO: python version did 100ms
				time.Sleep(1 * time.Second)
				OpenPin.Low()
			case cmd == "close":
				ClosePin.High()
				// TODO: python version did 100ms
				time.Sleep(1 * time.Second)
				ClosePin.Low()
			default:
				log.Println("unknown cmd received on CmdChan")
			}
		}
	}()
}

func main() {
	flag.Parse()

	log.Println(" :: Starting sphincter rfid token...")
	log.Println(" :::: Opening GPIO")
	err := rpio.Open()
	if err != nil {
		log.Fatal(err)
	}
	OpenPin.Output()
	ClosePin.Output()
	StatusPin0.Input()
	StatusPin1.Input()

	log.Println(" :::: Reading list.txt")
	users, err := parseUserList()
	if err != nil {
		log.Fatal(err)
	}
	log.Printf(" :::: Found %d users \n", len(users))
	// log.Printf("%v\n", users)

	log.Println(" :::: Connecting to Serial")
	mode := &serial.Mode{
		BaudRate: 9600,
	}
	port, err := serial.Open(*port, mode)
	if err != nil {
		log.Fatal(err)
	}

	log.Println(" :::: Setting up webserver")
	http.HandleFunc("/sphincter", func(w http.ResponseWriter, r *http.Request) {
		log.Println(r)
		if r.Method != "GET" {
			log.Println("Ignoring non-GET request.")
			return
		}
		//r.ParseForm()
		action := r.URL.Query().Get("action")
		//token := r.Form.Get("token")
		switch {
		case action == "state":
			fmt.Fprint(w, "UNLOCKED")
		case action == "unlock":
			// TODO: check token
			OpenPin.High()
			time.Sleep(1 * time.Second)
			OpenPin.Low()
			fmt.Fprint(w, "UNLOCKED")
		case action == "lock":
			// TODO: check token
			fmt.Fprint(w, "LOCKED")
		default:
			fmt.Fprint(w, "action parameter must be one of status, lock or unlock")
		}

	})
	http.ListenAndServe(":8001", nil)

	log.Println(" :: Initialized!")

	for msg := range getRFIDToken(&port) {
		if time.Since(latestTimestamp) < 5*time.Second {
			log.Println("Triggered too fast; skipped unlock")
			continue
		}

		username, ok := users[msg]
		if ok {
			latestTimestamp = time.Now()
			log.Printf("Hello %s %s", msg, username)
			OpenPin.High()
			time.Sleep(1 * time.Second)
			OpenPin.Low()
		} else {
			if isValid(msg) {
				log.Printf("Could not find key %s", msg)
			}
		}
	}
}
