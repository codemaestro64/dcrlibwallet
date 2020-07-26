package dcrlibwallet

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/gorilla/websocket"
)

const (
	websocketEndpoint = "proposals.decred.org"
	apiEndpoint       = "https://proposals.decred.org/api/v1"
)

func (mw *MultiWallet) getRequester(path string, method string, bodyStr string, headers map[string][]string) *http.Request {
	if headers == nil {
		headers = make(map[string][]string)
	}
	headers["X-CSRF-TOKEN"] = []string{"ddd"}

	return &http.Request{
		Method: method,
		URL:    url,
		Header: headers,
		Body:   ioutil.NopCloser(strings.NewReader(bodyStr)),
	}
}

func (mw *MultiWallet) post(path string, data interface{}, responseData interface{}) error {
	d, err := json.Marshal(data)
	if err != nil {
		return fmt.Errorf("error marshalling post data: %s", err.Error())
	}

	//r := mw.getRequester(path, "POST", string(d), nil).

	res, err := mw.httpClient.Do(url, "application/json", bytes.NewBuffer(d))
	if err != nil {
		return fmt.Errorf("error posting data: %s", err.Error())
	}
	defer res.Body.Close()

	b, _ := ioutil.ReadAll(res.Body)

	fmt.Println(string(b))

	return json.NewDecoder(res.Body).Decode(responseData)
}

func (mw *MultiWallet) get(url string, responseData interface{}) (*http.Response, error) {
	res, err := mw.httpClient.Get(url)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()

	return res, json.NewDecoder(res.Body).Decode(responseData)
}

func (mw *MultiWallet) GetVersion() (*VersionResponse, error) {
	var res *VersionResponse

	r, err := mw.get(apiEndpoint+"/version", &res)
	if err != nil {
		return nil, err
	}
	mw.token = r.Header.Get("x-csrf-token")
	fmt.Println(mw.token)

	return res, nil
}

func (mw *MultiWallet) Login(email, password string) (*LoginResponse, error) {
	_, err := mw.GetVersion()
	if err != nil {
		return nil, err
	}

	var res *LoginResponse

	req := User{
		Email:    email,
		Password: password,
	}

	err = mw.post(apiEndpoint+"/login", req, &res)
	if err != nil {
		return nil, err
	}

	return res, nil
}

func (mw *MultiWallet) openWsConn() error {
	u := url.URL{Scheme: "wss", Host: websocketEndpoint, Path: "/api/v1/aws"}

	conn, _, err := websocket.DefaultDialer.Dial(u.String(), nil)
	if err != nil {
		fmt.Println(u.String())
		return err
	}
	mw.wsConn = conn
	log.Info("successfully connected to proposals websocket server on %s", websocketEndpoint)

	return nil
}

func (mw *MultiWallet) closeWsConn() {
	mw.wsConn.Close()
}
