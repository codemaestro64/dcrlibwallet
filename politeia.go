package dcrlibwallet

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/ioutil"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/gorilla/websocket"
	"golang.org/x/sync/errgroup"
)

const (
	endpoint            = "proposals.decred.org"
	endpointPath        = "/api/v1"
	versionPath         = "/version"
	policyPath          = "/policy"
	vettedProposalsPath = "/proposals/vetted"
	proposalDetailsPath = "/proposals/%s"
	voteStatusPath      = "/proposals/%s/votestatus"
	votesStatusPath     = "/proposals/votestatus"
	loginPath           = "/login"
	wsPath              = endpointPath + "/aws"
)

type header struct {
	name      string
	value     string
	expiresAt time.Time
}

type Politeia struct {
	serverPolicy         *ServerPolicy
	notificationListener ProposalNotificationListener
	requestHeaders       map[string][]header
	user                 User

	wsConn *websocket.Conn
}

func NewPoliteia() Politeia {
	p := Politeia{
		requestHeaders: make(map[string][]header),
	}
	return p
}

func (p *Politeia) prepareRequest(path, method string, queryStrings map[string]string, body []byte) (*http.Request, error) {
	req := &http.Request{
		Method: method,
		URL:    &url.URL{Scheme: "https", Host: endpoint, Path: endpointPath + path},
	}

	if body != nil {
		req.Body = ioutil.NopCloser(bytes.NewBuffer(body))
	}

	if queryStrings != nil {
		queryString := req.URL.Query()
		for i, v := range queryStrings {
			queryString.Set(i, v)
		}
		req.URL.RawQuery = queryString.Encode()
	}

	if method == "POST" {
		if _, ok := p.requestHeaders["X-CSRF-TOKEN"]; !ok {
			if err := p.getCSRFToken(); err != nil {
				return nil, err
			}
		}
		p.setRequestHeaders(req)
	}

	return req, nil
}

func (p *Politeia) setRequestHeaders(req *http.Request) {
	req.Header = make(http.Header)
	for key, val := range p.requestHeaders {
		switch key {
		case "X-CSRF-TOKEN":
			req.Header.Set("X-CSRF-TOKEN", val[0].value)
		case "cookies":
			cookieStr := ""
			for _, ck := range val {
				cookieStr += ck.name + "=" + ck.value + ";"
			}
			req.Header.Set("cookie", cookieStr)
		}
	}
}

func (p *Politeia) getCSRFToken() error {
	req := &http.Request{
		Method: "GET",
		URL:    &url.URL{Scheme: "https", Host: endpoint, Path: endpointPath + versionPath},
	}

	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("error fetching csrf token")
	}
	p.requestHeaders["X-CSRF-TOKEN"] = []header{{value: res.Header.Get("X-CSRF-TOKEN")}}

	for _, v := range res.Cookies() {
		ck := header{
			name:      v.Name,
			value:     v.Value,
			expiresAt: v.Expires,
		}
		p.requestHeaders["cookies"] = append(p.requestHeaders["cookies"], ck)
	}

	return nil
}

func (p *Politeia) makeRequest(path, method string, queryStrings map[string]string, body []byte, dest interface{}) error {
	req, err := p.prepareRequest(path, method, queryStrings, body)
	if err != nil {
		return err
	}

	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}

	sessionCookie := res.Header.Get("Set-Cookie")
	if sessionCookie != "" {
		s := strings.Split(strings.Replace(sessionCookie, "session=", "", -1), ";")
		h := header{
			name:  "session",
			value: s[0],
		}
		p.requestHeaders["cookies"] = append(p.requestHeaders["cookies"], h)
	}

	return p.handleResponse(res, dest)
}

func (p *Politeia) handleResponse(res *http.Response, dest interface{}) error {
	switch res.StatusCode {
	case http.StatusOK:
		return json.NewDecoder(res.Body).Decode(dest)
	case http.StatusNotFound:
		return errors.New("resource not found")
	case http.StatusInternalServerError:
		return errors.New("internal server error")
	case http.StatusUnauthorized:
		var errResp Err
		if err := p.marshalResponse(res, &errResp); err != nil {
			return err
		}
		return fmt.Errorf(ErrorStatus[errResp.Code])
	case http.StatusBadRequest:
		var errResp Err
		if err := p.marshalResponse(res, &errResp); err != nil {
			return err
		}
		return fmt.Errorf(ErrorStatus[errResp.Code])
	}

	return errors.New("an unknown error occurred")
}

func (p *Politeia) marshalResponse(res *http.Response, dest interface{}) error {
	defer res.Body.Close()

	err := json.NewDecoder(res.Body).Decode(dest)
	if err != nil {
		return fmt.Errorf("error decoding response body: %s", err.Error())
	}

	return nil
}

func (p *Politeia) getServerPolicy() (*ServerPolicy, error) {
	var serverPolicy ServerPolicy

	err := p.makeRequest(policyPath, "GET", nil, nil, &serverPolicy)
	if err != nil {
		return nil, fmt.Errorf("error fetching politeia policy: %v", err)
	}
	return &serverPolicy, nil
}

func (p *Politeia) getProposalsChunk(startHash string) ([]Proposal, error) {
	var queryStrings map[string]string
	if startHash != "" {
		queryStrings = map[string]string{
			"after": startHash,
		}
	}

	var result Proposals
	err := p.makeRequest(vettedProposalsPath, "GET", queryStrings, nil, &result)
	if err != nil {
		return nil, fmt.Errorf("error fetching proposals from %s: %v", startHash, err)
	}

	return result.Proposals, err
}

// GetProposalsChunk gets proposals starting after the proposal with the specified
// censorship hash. The number of proposals returned is specified in the poltieia
// policy API endpoint
func (p *Politeia) GetProposalsChunk(startHash string) (string, error) {
	proposals, err := p.getProposalsChunk(startHash)
	if err != nil {
		return "", err
	}

	wg, _ := errgroup.WithContext(context.Background())
	for i := range proposals {
		i := i
		wg.Go(func() error {
			voteStatus, err := p.getVoteStatus(proposals[i].CensorshipRecord.Token)
			if err != nil {
				return err
			}
			proposals[i].VoteStatus = *voteStatus
			return nil
		})
	}

	if err := wg.Wait(); err != nil {
		return "", err
	}

	jsonBytes, err := json.Marshal(proposals)
	if err != nil {
		return "", fmt.Errorf("error marshalling proposal result to json: %s", err.Error())
	}

	return string(jsonBytes), nil
}

// GetAllProposal fetches all vetted proposals
func (p *Politeia) GetAllProposals() (string, error) {
	var proposalChunkResult, proposals []Proposal
	var err error

	if p.serverPolicy == nil {
		policy, err := p.getServerPolicy()
		if err != nil {
			return "", err
		}
		p.serverPolicy = policy
	}

	proposalChunkResult, err = p.getProposalsChunk("")
	if err != nil {
		return "", fmt.Errorf("error fetching all proposals: %s", err.Error())
	}
	proposals = append(proposals, proposalChunkResult...)

	for {
		if proposalChunkResult == nil || len(proposalChunkResult) < p.serverPolicy.ProposalListPageSize {
			break
		}

		proposalChunkResult, err = p.getProposalsChunk(proposalChunkResult[p.serverPolicy.ProposalListPageSize-1].CensorshipRecord.Token)
		if err != nil {
			return "", err
		}
		proposals = append(proposals, proposalChunkResult...)
	}

	jsonBytes, err := json.Marshal(proposals)
	if err != nil {
		return "", fmt.Errorf("error marshalling proposal result to json: %s", err.Error())
	}

	return string(jsonBytes), err
}

// GetProposalDetails fetches the details of a single proposal
// if the version argument is an empty string, the latest version is used
func (p *Politeia) GetProposalDetails(censorshipToken, version string) (string, error) {
	if censorshipToken == "" {
		return "", errors.New("censorship token cannot be empty")
	}

	var queryStrings map[string]string
	if version != "" {
		queryStrings = map[string]string{
			"version": version,
		}
	}

	var result ProposalResult
	err := p.makeRequest(fmt.Sprintf(proposalDetailsPath, censorshipToken), "GET", queryStrings, nil, &result)
	if err != nil {
		return "", err
	}

	jsonBytes, err := json.Marshal(result.Proposal)
	if err != nil {
		return "", fmt.Errorf("error marshalling proposal result to json: %s", err.Error())
	}

	return string(jsonBytes), err
}

func (p *Politeia) getVoteStatus(censorshipToken string) (*VoteStatus, error) {
	if censorshipToken == "" {
		return nil, errors.New("censorship token cannot be empty")
	}

	var voteStatus VoteStatus
	err := p.makeRequest(fmt.Sprintf(voteStatusPath, censorshipToken), "GET", nil, nil, &voteStatus)
	if err != nil {
		return nil, err
	}

	return &voteStatus, nil
}

// GetVoteStatus fetches the vote status of a single public proposal
func (p *Politeia) GetVoteStatus(censorshipToken string) (string, error) {
	voteStatus, err := p.getVoteStatus(censorshipToken)
	if err != nil {
		return "", err
	}

	jsonBytes, err := json.Marshal(voteStatus)
	if err != nil {
		return "", fmt.Errorf("error marshalling proposal result to json: %s", err.Error())
	}

	return string(jsonBytes), nil
}

// GetAllVotesStatus fetches the vote status of all public proposals
func (p *Politeia) GetAllVotesStatus() (string, error) {
	var votesStatus VotesStatus
	err := p.makeRequest(votesStatusPath, "GET", nil, nil, &votesStatus)
	if err != nil {
		return "", err
	}

	jsonBytes, err := json.Marshal(votesStatus)
	if err != nil {
		return "", fmt.Errorf("error marshalling proposal result to json: %s", err.Error())
	}

	return string(jsonBytes), nil
}
