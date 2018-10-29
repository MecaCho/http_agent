package install_agent_cert

import (
	"glog"
	"bytes"
	"net/http"
	"io/ioutil"
	"net/url"
	"httprouter"
	"strings"
	"fmt"
	"runtime"
	"reflect"
	"io"
	"crypto/x509"
)

const upgradeURLinEdgeMgr  = "%s/edgemgr_internal/nodes/%s/installinfo"

// serverResponse is a wrapper for http API responses.
type serverResponse struct {
	body       []byte
	header     http.Header
	statusCode int
	reqURL     *url.URL
}

type header map[string][]string

//InstallInfoResponse node install get plan version from edgemanager
type InstallInfoResponse struct {
	MajorVersion    int                    `json:"major_version"`
	MinorVersion    int                    `json:"minor_version"`
	FixVersion      int                    `json:"fix_version"`
	BuildVersion    string                 `json:"build_version"`
	Package         string                 `json:"package"`
	StartParameters map[string]interface{} `json:"start_parameters"`
}

//NodeInstallStatusPut node install put upgrade result to edgemanager
type NodeInstallStatusPut struct {
	MajorVersion string `json:"major_version"`
	MinorVersion string `json:"minor_version"`
	FixVersion   string `json:"fix_version"`
	BuildVersion string `json:"build_version"`
	Status       string `json:"status"`
	Reason       string `json:"reason"`
}

type upgradeHandle func(http.ResponseWriter, *http.Request, string)

type EMClient struct {
	edgeAPIURL     string
	edgeAPIVersion string
	verifyOpts     x509.VerifyOptions
	edgeClient     http.Client
}

func (ph *EMClient) validateCert(r *http.Request) error {
	if r.TLS == nil || len(r.TLS.PeerCertificates) == 0 {
		return fmt.Errorf("no tls certificate provided")
	}
	_, err := r.TLS.PeerCertificates[0].Verify(ph.verifyOpts)
	return err
}

func (ph *EMClient)CheckAuthorition(this upgradeHandle) httprouter.Handle {
	return func(w http.ResponseWriter, r *http.Request, p httprouter.Params){
		//err := ph.validateCert(r)
		//if err != nil {
		//	glog.Error(err.Error())
		//	http.Error(w, "", http.StatusUnauthorized)
		//	return
		//}
		//name, projectID, err := getUserInfoFromCert(r.TLS.PeerCertificates[0])
		//if err != nil {
		//	glog.Errorf("Check user info error, project: %s, id: %s, error message: %s", projectID, name, err.Error())
		//	http.Error(w, "", http.StatusBadRequest)
		//	return
		//}
		//nodeID := p.ByName("node_id")
		//if (nodeID != name) {
		//	glog.Errorf("Check user info error, project: %s, id: %s, error message: %s", projectID, name, "NodeID NOT MATCH")
		//	http.Error(w, "Node Cert Unauthorized", http.StatusBadRequest)
		//	return
		//}
		//if !strings.Contains(r.URL.Path, projectID) {
		//	glog.Errorf("Check user info error, project: %s, id: %s, error message: %s", projectID, name, "ProjectID NOT MATCH")
		//	http.Error(w, "Node Cert Unauthorized", http.StatusBadRequest)
		//	return
		//}
		projectID := ""
		nodeID := ""
		var url string
		if strings.Contains(r.URL.Path, "nodestatus"){
			url = fmt.Sprintf(upgradeURLinEdgeMgr+"/nodestatus", projectID, nodeID)
		}else {
			installType := p.ByName("type")
			if (installType != "install") && (installType != "upgrade")  {
				glog.Errorf("Get install plan version info error, project: %s, id: %s, error message: %s", projectID, nodeID, "WRONG INSTALL TYPE")
				http.Error(w, "installType error", http.StatusBadRequest)
				return
			}
			url = fmt.Sprintf(upgradeURLinEdgeMgr+"/%s", projectID, nodeID, installType)
		}
		glog.Infof("Request url : %s", url)
		this(w, r, url)
	}
}

func LogUpgrade(this upgradeHandle) upgradeHandle {
	return func(w http.ResponseWriter, r *http.Request, url string) {
		funcName := runtime.FuncForPC(reflect.ValueOf(this).Pointer()).Name()
		glog.Infoln("Node upgrade func called : ", funcName)
		glog.Infof("Request from edged host: %s", r.Host)
		this(w, r, url)
	}
}

func (ph *EMClient)installerGetNodeSatus(w http.ResponseWriter, r *http.Request, url string) {
	url = strings.Join([]string{ph.edgeAPIURL,ph.edgeAPIVersion,url}, "/")
	req, err := buildRequest(http.MethodGet, url, nil, nil)
	if err != nil{
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	resp, err := doRequest(&ph.edgeClient, req)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(resp.statusCode)
	glog.Infoln("Get node status ret, response :", string(resp.body))
	fmt.Fprintf(w, string(resp.body))
}

func (ph *EMClient)installerPutResult(w http.ResponseWriter, r *http.Request, upgradeURLinEdgeMgr string) {
	upgradeURLinEdgeMgr = strings.Join([]string{ph.edgeAPIURL,ph.edgeAPIVersion,upgradeURLinEdgeMgr}, "/")
	req, err := buildRequest(http.MethodPut, upgradeURLinEdgeMgr, r.Body, nil)
	if err != nil{
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	resp, err := doRequest(&ph.edgeClient, req)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(resp.statusCode)
	glog.Infoln("Get node status ret, response :", string(resp.body))
	fmt.Fprintf(w, string(resp.body))
}

func (ph *EMClient)installerGetVersion(w http.ResponseWriter, r *http.Request, upgradeURLinEdgeMgr string){
	upgradeURLinEdgeMgr = strings.Join([]string{ph.edgeAPIURL,ph.edgeAPIVersion,upgradeURLinEdgeMgr}, "/")
	req, err := buildRequest(http.MethodPost, upgradeURLinEdgeMgr, r.Body, nil)
	if err != nil{
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	resp, err := doRequest(&ph.edgeClient, req)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(resp.statusCode)
	glog.Infoln("Get node status ret, response :", string(resp.body))
	fmt.Fprintf(w, string(resp.body))
}

func buildRequest(method, path string, body io.Reader, headers header) (req *http.Request, err error){
	if body != nil{
		requestBody, err := ioutil.ReadAll(body)
		if err != nil {
			glog.Errorf("Build request error, %s", err.Error())
			return nil, err
		}
		body = bytes.NewReader(requestBody)
	}
	expectedPayload := (method == "POST" || method == "PUT")
	if expectedPayload && body == nil {
		body = bytes.NewReader([]byte{})
	}
	req, err = http.NewRequest(method, path, body)
	if err != nil {
		return nil, err
	}
	if headers != nil {
		for k, v := range headers {
			req.Header[k] = v
		}
	}
	if expectedPayload && req.Header.Get("Content-Type") == "" {
		req.Header.Set("Content-Type", "application/json")
	}
	glog.Infoln("New request : ", path)
	return
}

func doRequest(cli *http.Client, r *http.Request) (serverResp serverResponse, err error){
	serverResp = serverResponse{statusCode: -1}
	resp, err := cli.Do(r)
	if err != nil {
		glog.Errorf("Do request error, %s", err.Error())
		return serverResp, err
	}
	serverResp.statusCode = resp.StatusCode
	defer resp.Body.Close()
	serverResp.body, err = ioutil.ReadAll(resp.Body)
	if err != nil {
		glog.Errorln("Get response error", err.Error())
		return serverResp, err
	}
	return
}


