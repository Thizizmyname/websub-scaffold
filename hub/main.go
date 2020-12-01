package main

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io/ioutil"
	"log"
	"net/http"
	"time"
)

// Map with a topic as key, and each topic having a subsequent map of subscribers and their secret.
var subscriberMap map[string]map[string]string

// Helper structure to assist in logging
type logType string

// Enum to handle logging a little neater
const (
	Info    logType = "info"
	Error           = "error"
	Warning         = "warning"
)

func hubLog(ty logType, message string) {
	t := time.Now().Format("2006-01-02 15:04:05")
	fmt.Printf("time=\"%v\"  level=%v msg=%v\n", t, ty, message)
}

/* handleRequest
 * takes a request and determines what type of request it is.
 * if a subscribe-event, a confirmation is required before adding the requester as subscriber.
 */
func handleRequest(mode, topic, callback, secret string) (bool, error) {
	valid := false
	var err error

	switch mode {
	case "unsubscribe":

		if _, ok := subscriberMap[topic]; ok {
			delete(subscriberMap[topic], callback)
		}
		// In this example no logic to remove a topic is implemented
		valid = true

	case "subscribe":

		// Confirm request before adding subscriber
		valid = confirmRequest(callback, topic)

		if valid {
			// Create topic map if topic is not known.
			// Probably the publisher should have the authority to create and remove topics
			// and not a subscriber, but it is this way for the example to work.
			if _, ok := subscriberMap[topic]; !ok {
				subscriberMap[topic] = make(map[string]string)
			}

			// Add client or update secret
			subscriberMap[topic][callback] = secret
		}

	default:
		err = fmt.Errorf("[Request Validation] mode \"%v\" not supported", mode)
	}

	return valid, err
}

/* denyRequest
 * Sends a denial of request, includes reason if given.
 */
func denyRequest(callback, topic, reason string) {
	client := &http.Client{}
	req, _ := http.NewRequest("GET", callback, nil)

	q := req.URL.Query()
	q.Add("hub.mode", "denied")
	q.Add("hub.topic", topic)
	if reason != "" {
		q.Add("hub.reason", reason)
	}
	req.URL.RawQuery = q.Encode()

	resp, err := client.Do(req)

	if err != nil {
		hubLog(Error, "Error when sending request to the server")
		return
	}
	defer resp.Body.Close()

}

/* confirmRequest
 * Sends a subscription confirmation request with challenge.
 * Returns true if confirmation is received with the challenge-token.
 */
func confirmRequest(callback, topic string) bool {
	client := &http.Client{}
	req, _ := http.NewRequest("GET", callback, nil)

	// TODO: update challenge to random string
	challenge := "challenge:" + topic

	// send confirmation with challenge
	q := req.URL.Query()
	q.Add("hub.mode", "subscribe")
	q.Add("hub.topic", topic)
	q.Add("hub.challenge", challenge)
	//Required field in documentation, this example does nothing with it
	q.Add("hub.lease_seconds", "123")
	req.URL.RawQuery = q.Encode()

	resp, err := client.Do(req)

	if err != nil {
		hubLog(Error, "Error when sending request to the server")
		return false
	}
	defer resp.Body.Close()

	// Read Response Body
	respBody, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		hubLog(Error, fmt.Sprintf("[HEALTH CHECK] Could not read response body : %v", err))
		return false
	}
	// If client does not return correct result,
	// consider as failed
	if resp.Status != "200 OK" || challenge != string(respBody) {
		hubLog(Info, fmt.Sprintf("[RESPONSE CHECK] Response considered as failed : [%v] %v", resp.Status, string(respBody)))
		return false
	}

	// Validation correct
	hubLog(Info, fmt.Sprintf("[RESPONSE CHECK] Response valid : %v", callback))
	return true
}

/* hubRequest
 * Endpoint for "/"
 * only accepts POST requests with a form
 * note: no validation upon the forms values are properly done
 */
func hubRequest(w http.ResponseWriter, r *http.Request) {
	// This rudimentary hub only takes care of post-requests from a websub-client
	if r.Method == "POST" {

		if err := r.ParseForm(); err != nil {
			fmt.Println("ParseForm() err:", err)
			return
		}
		// Required fields
		mode := r.FormValue("hub.mode")
		topic := r.FormValue("hub.topic")
		callback := r.FormValue("hub.callback")

		// Optional
		secret := r.FormValue("hub.secret")
		// This hub does not respect lease timing
		// lease := r.FormValue("hub.lease_seconds")

		hubLog(Info, fmt.Sprintf("%v request from %v", mode, callback))

		accepted, err := handleRequest(mode, topic, callback, secret)

		// If request is denied for any reason, send a denial
		if accepted == false {
			hubLog(Info, err.Error())

			denyRequest(callback, topic, err.Error())
		}

	}
}

/* publish
 * Example endpoint to broadcast a hardcoded post to topic "/a/topic"
 */
func publish(w http.ResponseWriter, r *http.Request) {
	client := &http.Client{}

	// Topic and message is hardcoded in this example publishing
	topic := "/a/topic"
	var jsonStr = []byte(`{"title":"This is an example post"}`)

	hubLog(Info, "Broadcasting example message on topic: \"/a/topic\"")

	// Iterate through every subscriber
	for subscriber, secret := range subscriberMap[topic] {
		req, err := http.NewRequest("POST", subscriber, bytes.NewBuffer(jsonStr))

		// Generate HMAC
		mac := hmac.New(sha256.New, []byte(secret))
		mac.Write(jsonStr)
		hmac := hex.EncodeToString(mac.Sum(nil))

		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-Hub-Signature", "sha256="+hmac)

		resp, err := client.Do(req)
		if err != nil {
			panic(err)
		}
		defer resp.Body.Close()

		if resp.Status != "200 OK" {
			hubLog(Error, fmt.Sprintf("[Publish Error] Response status : [%v] at %v", resp.Status, subscriber))
		}
	}
}

func main() {
	subscriberMap = make(map[string]map[string]string)

	http.HandleFunc("/", hubRequest)
	http.HandleFunc("/publish", publish)

	hubLog(Info, "Starting hub and accessible for requests")
	if err := http.ListenAndServe(":8080", nil); err != nil {
		log.Fatal(err)
	}
}
