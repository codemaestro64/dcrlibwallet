package dcrlibwallet

import (
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"net/http"
	"net/url"

	"github.com/gorilla/websocket"
	"golang.org/x/crypto/sha3"
)

type ProposalNotificationListener interface {
	OnNewProposal(proposalJsonObject string)
	OnProposalVoteStarted(proposalJsonObject string)
	OnProposalVoteEnded(proposalJsonObject string)
}

func (p *Politeia) openWsConn() error {
	u := &url.URL{Scheme: "wss", Host: endpoint, Path: wsPath}

	req := &http.Request{}
	p.setRequestHeaders(req)

	conn, resp, err := websocket.DefaultDialer.Dial(u.String(), req.Header)
	if err != nil {
		e, _ := ioutil.ReadAll(resp.Body)
		fmt.Println(string(e))
		return err
	}

	if resp.StatusCode != 200 || resp.StatusCode != 101 {
		err = p.handleResponse(resp, nil)
		if err != nil {
			return err
		}
	}
	p.wsConn = conn

	return nil
}

func (p *Politeia) login(email, password string) error {
	pwd := sha3.Sum256([]byte(password))

	login := Login{
		Email:    email,
		Password: hex.EncodeToString(pwd[:]),
	}

	body, err := json.Marshal(login)
	if err != nil {
		return fmt.Errorf("error marshaling request body: %s", err.Error())
	}

	var user User
	err = p.makeRequest(loginPath, "POST", nil, body, &user)
	if err != nil {
		return err
	}
	p.user = user

	return nil
}

func (p *Politeia) AddNotificationListener(email, password string, listener ProposalNotificationListener) error {
	err := p.login(email, password)
	if err != nil {
		return err
	}

	err = p.openWsConn()
	if err != nil {
		return err
	}

	p.notificationListener = listener

	doneChan := make(chan struct{})
	go p.readWsMessages(doneChan)
	go p.writeMessages()

	return nil
}

func (p *Politeia) readWsMessages(doneChan chan struct{}) {
	go func() {
		defer close(doneChan)
		for {
			_, message, err := p.wsConn.ReadMessage()
			if err != nil {
				return
			}

			// TODO marshal message into our ws response struct and then call
			// the appropriate notification listener
		}
	}()
}

func (p *Politeia) writeMessages() {

}

func (p *Politeia) onNewProposal(proposalJsonObject string) {
	p.notificationListener.OnNewProposal(proposalJsonObject)
}

func (p *Politeia) onProposalVoteStarted(proposalJsonObject string) {
	p.notificationListener.OnProposalVoteStarted(proposalJsonObject)
}

func (p *Politeia) onProposalVoteEnded(proposalJsonObject string) {
	p.notificationListener.OnProposalVoteEnded(proposalJsonObject)
}
