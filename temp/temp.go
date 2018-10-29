package temp

import (
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"glog"
	"github.com/jinzhu/gorm"
	"k8s.io/kubernetes/pkg/api"
	clientset "k8s.io/kubernetes/pkg/client/clientset_generated/internalclientset"
	"k8s.io/kubernetes/pkg/client/unversioned/clientcmd"
	"github.com/httprouter"
	"bytes"
)

// Keywords for extracting user information from certificate
const (
	CertUserNameKey  string = "1.1.1.1"
	CertProjectIDKey string = "1.1.1.4"
	UserInfoPrefix   string = "system:node:"
	NodeStateStopped string = "STOPPED"
)

// Cache token and project
var cacheToken string
var cacheProject string

// Cache metrics
var metrics sync.Map

// Handle represents the handler for placement service
type Handle struct {
	edgeAPIURL         string
	edgeAPIVersion     string
	verifyOpts         x509.VerifyOptions
	edgeClient         http.Client
	serviceClient      http.Client
	mqClient           http.Client
	endpoints          []*Endpoint
	kubeClients        []*clientset.Clientset
	queues             []*QueueEndpoint
	serviceEndpoints   map[string]string
	authSyncInterval   int
	metricSyncInterval int
	db                 *gorm.DB
}

// EdgeNode represents a edge node
type EdgeNode struct {
	Name          string `json:"name"`
	Description   string `json:"description"`
	ProjectID     string `json:"project_id"`
	MasterAddr    string `json:"master_addr"`
	DockerRootDir string `json:"docker_root_dir"`
}

// Endpoint represents a pair of internal and external master url
type Endpoint struct {
	Index       int
	InternalURL string
	ProxyURL    string
	ExternalURL string
}

// QueueEndpoint represents a pair of internal and external message queue(or access) url
type QueueEndpoint struct {
	Index           int
	AccessURL       string
	MQUrlForPod     string
	MQUrlForEdged   string
	MQUrlForManager string
}

func constructVerifyOpts(config *Config) (x509.VerifyOptions, error) {
	var opts x509.VerifyOptions
	pool := x509.NewCertPool()
	ok := pool.AppendCertsFromPEM(config.caData)
	if !ok {
		return opts, newCertPoolCreateError()
	}
	opts = x509.VerifyOptions{
		Roots: pool,
		KeyUsages: []x509.ExtKeyUsage{
			x509.ExtKeyUsageServerAuth,
			x509.ExtKeyUsageClientAuth},
	}
	return opts, nil
}

func newPlacementHandle(config *Config) (*Handle, error) {
	db, err := gorm.Open("mysql", config.dbConnStr)
	if err != nil {
		return nil, err
	}

	opts, err := constructVerifyOpts(config)
	if err != nil {
		return nil, err
	}
	// edge master init
	endpoints := make([]*Endpoint, len(config.kubeAPIURLs))
	clients := make([]*clientset.Clientset, len(config.kubeAPIURLs))
	for i, internalURL := range config.kubeAPIURLs {
		clientConfig, err := clientcmd.BuildConfigFromFlags(internalURL, config.kubeConfig)
		if err != nil {
			return nil, err
		}
		clientSet, err := clientset.NewForConfig(clientConfig)
		if err != nil {
			return nil, err
		}
		endpoints[i] = &Endpoint{
			Index:       i,
			InternalURL: internalURL,
			ProxyURL:    config.kubeAPIForManagers[i],
			ExternalURL: config.kubeAPIForEdgeds[i]}
		clients[i] = clientSet
	}

	// message queue init
	queues := make([]*QueueEndpoint, len(config.accessAPIURLs))
	for i, accessURL := range config.accessAPIURLs {
		queues[i] = &QueueEndpoint{
			Index:           i,
			AccessURL:       accessURL,
			MQUrlForPod:     config.queueAPIForPods[i],
			MQUrlForEdged:   config.queueAPIForEdgeds[i],
			MQUrlForManager: config.queueAPIForManager[i],
		}
	}

	glog.Infof("queue is %v\n", queues)

	tp := &http.Transport{
		TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
	}

	// config for https
	cert, err := tls.X509KeyPair(config.certData, config.keyData)
	if err != nil {
		return nil, err
	}
	mqTp := &http.Transport{
		TLSClientConfig: &tls.Config{
			Certificates:       []tls.Certificate{cert},
			RootCAs:            opts.Roots,
			InsecureSkipVerify: true,
		},
	}
	handle := Handle{
		edgeAPIURL:         config.edgeAPIURL,
		edgeAPIVersion:     config.edgeAPIVersion,
		verifyOpts:         opts,
		edgeClient:         http.Client{Timeout: time.Second * 30, Transport: tp},
		serviceClient:      http.Client{Timeout: time.Second * 10, Transport: tp},
		mqClient:           http.Client{Timeout: time.Second * 30, Transport: mqTp},
		queues:             queues,
		endpoints:          endpoints,
		kubeClients:        clients,
		serviceEndpoints:   config.endpoints,
		authSyncInterval:   config.authSyncInterval,
		metricSyncInterval: config.metricSyncInterval,
		db:                 db,
	}
	return &handle, nil
}

func (ph *Handle) getEndpointByProxyURL(url string) *Endpoint {
	// the number of endpoints are small, so we simply scan the list
	for _, endpoint := range ph.endpoints {
		if endpoint.ProxyURL == url {
			return endpoint
		}
	}
	return nil
}

func (ph *Handle) scheduleKubeAPIURL() int {
	index := -1
	minNode := -1
	opts := api.ListOptions{}
	for i, client := range ph.kubeClients {
		nodes, err := client.Nodes("").List(opts)
		if err != nil {
			glog.Errorf("fail to get nodes from %s, reason %s", ph.endpoints[i].InternalURL, err.Error())
			continue
		}
		node := len(nodes.Items)
		if index == -1 || node < minNode {
			index = i
			minNode = node
		}
	}
	return index
}

func (ph *Handle) ensureNamespace(name string, index int) error {
	n := ph.kubeClients[index].Namespaces()
	_, err := n.Get(name)
	if err == nil {
		return err
	}
	if !strings.Contains(err.Error(), "not found") {
		return err
	}
	ns := api.Namespace{
		ObjectMeta: api.ObjectMeta{Name: name},
	}
	_, err = n.Create(&ns)
	return err
}

func (ph *Handle) getEdgeNodeMaster(name, project string) (string, error) {
	url := fmt.Sprintf("%s/v1/%s/edgemgr_internal/edge_devices/%s", ph.edgeAPIURL, project, name)
	resp, err := ph.edgeClient.Get(url)
	if err != nil {
		return "", err
	}
	switch resp.StatusCode {
	case http.StatusOK:
		defer resp.Body.Close()
		data, err := ioutil.ReadAll(resp.Body)
		if err != nil {
			return "", err
		}
		body := make(map[string]interface{})
		json.Unmarshal(data, &body)
		node := body["edge_device"].(map[string]interface{})
		if node["state"] == NodeStateStopped {
			return "", ErrorNodeStateStopped
		}
		if node["master_addr"] == nil {
			return "", nil
		}
		return node["master_addr"].(string), nil
	case http.StatusNotFound:
		return "", nil
	default:
		return "", newEdgeNodeMasterGetError()
	}
}

func (ph *Handle) syncServiceAuthInfo(startSync, stop chan bool) {
	for i := 0; i < 5; i++ {
		token, projectID, err := ph.getServiceAuthInfo()
		if err == nil {
			cacheToken = token
			cacheProject = projectID
			break
		}
		glog.Errorf("fail to retrieve token and project, reason: %s, retry", err.Error())
		time.Sleep(2 * time.Second)
	}
	if cacheToken == "" || cacheProject == "" {
		panic("failt to retrieve token and project at starting")
	}
	startSync <- true

	func() {
		for {
			select {
			case <-stop:
				return
			default:
			}
			time.Sleep(time.Duration(ph.authSyncInterval) * time.Second)
			for {
				token, projectID, err := ph.getServiceAuthInfo()
				if err == nil {
					cacheToken = token
					cacheProject = projectID
					break
				}
				// always retry
				glog.Errorf("fail to retrieve token and project, reason: %s, retry", err.Error())
				time.Sleep(2 * time.Second)
			}
		}
	}()
	glog.Info("token project sync goroutine exit")
}

func (ph *Handle) getServiceAuthInfo() (string, string, error) {
	url := fmt.Sprintf("%s/v1/project_id/edgemgr_internal/tokens", ph.edgeAPIURL)
	resp, err := ph.edgeClient.Get(url)
	if err != nil {
		return "", "", err
	}
	if resp.StatusCode == http.StatusOK {
		defer resp.Body.Close()
		data, err := ioutil.ReadAll(resp.Body)
		if err != nil {
			return "", "", err
		}
		body := make(map[string]interface{})
		json.Unmarshal(data, &body)
		token := body["token"].(map[string]interface{})
		projectID := token["project_id"].(string)
		content := token["content"].(string)
		return content, projectID, nil
	}
	return "", "", newAuthInfoGetError()
}

func (ph *Handle) updateEdgeNodeMaster(node *EdgeNode) error {
	body := fmt.Sprintf("{\"edge_device\": {\"master_addr\": \"%s\", \"docker_root_dir\": \"%s\"}}",
		node.MasterAddr, node.DockerRootDir)
	url := fmt.Sprintf("%s/v1/%s/edgemgr_internal/edge_devices/%s", ph.edgeAPIURL, node.ProjectID, node.Name)
	req, err := http.NewRequest(http.MethodPut, url, strings.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := ph.edgeClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return newEdgeNodeMasterUpdateError()
	}
	return nil
}

func (ph *Handle) validateCert(r *http.Request) error {
	if r.TLS == nil || len(r.TLS.PeerCertificates) == 0 {
		return fmt.Errorf("no tls certificate provided")
	}
	_, err := r.TLS.PeerCertificates[0].Verify(ph.verifyOpts)
	return err
}

func (ph *Handle) serveCSPayload(w http.ResponseWriter, r *http.Request) {
	glog.Info("Post cs payload request")
	if r.Method != http.MethodPost {
		http.Error(w, "", http.StatusMethodNotAllowed)
		return
	}
	err := ph.validateCert(r)
	if err != nil {
		glog.Error(err.Error())
		http.Error(w, "", http.StatusUnauthorized)
		return
	}
	body, err := ioutil.ReadAll(r.Body)
	if err != nil {
		glog.Error(err.Error())
		http.Error(w, "", http.StatusBadRequest)
		return
	}
	var data map[string]interface{}
	err = json.Unmarshal(body, &data)
	if err != nil {
		glog.Error(err.Error())
		http.Error(w, "", http.StatusBadRequest)
		return
	}
	svc := data["svc_type"].(string)
	base, ok := ph.serviceEndpoints[svc]
	if !ok {
		http.Error(w, "not supported service", http.StatusBadRequest)
		return
	}
	base = strings.Trim(base, "/")
	uri := strings.Trim(data["uri"].(string), "/")
	payload := data["payload"].(string)
	token, project := cacheToken, cacheProject

	url := fmt.Sprintf("%s/%s/%s", base, project, uri)
	req, err := http.NewRequest(http.MethodPost, url, strings.NewReader(payload))
	req.Header.Set("Content-type", "application/json;charset=utf8")
	req.Header.Set("X-Auth-Token", token)
	if err != nil {
		glog.Error(err.Error())
		http.Error(w, "fail to construct payload request", http.StatusInternalServerError)
		return
	}

	glog.V(5).Infof("post body is %s", payload)

	resp, err := ph.serviceClient.Do(req)
	if err != nil {
		glog.Error(err.Error())
		http.Error(w, "fail to post payload", http.StatusInternalServerError)
		return
	}
	if resp.StatusCode != http.StatusOK {
		http.Error(w, fmt.Sprintf("fail to post payload, %d returned", resp.StatusCode),
			http.StatusInternalServerError)
		return
	}
	io.WriteString(w, "ok")
}

func extractDockerRootDir(r *http.Request) (string, error) {
	body, err := ioutil.ReadAll(r.Body)
	if err != nil {
		return "", err
	}
	var data map[string]interface{}
	err = json.Unmarshal(body, &data)
	if err != nil {
		return "", err
	}
	v, ok := data["docker_root_dir"]
	if !ok {
		return "", fmt.Errorf("expected key docker_root_dir not found")
	}
	return strings.TrimSpace(v.(string)), nil
}

func (ph *Handle) serveGetMaster(w http.ResponseWriter, r *http.Request) {
	glog.Info("Get api server url request")
	err := ph.validateCert(r)
	if err != nil {
		glog.Error(err.Error())
		http.Error(w, "", http.StatusUnauthorized)
		return
	}
	name, projectID, err := getUserInfoFromCert(r.TLS.PeerCertificates[0])
	if err != nil {
		glog.Error(err.Error())
		http.Error(w, "", http.StatusBadRequest)
		return
	}

	rootDir, err := extractDockerRootDir(r)
	if err != nil {
		glog.Error(err.Error())
		http.Error(w, "", http.StatusBadRequest)
		return
	}

	url, err := ph.getEdgeNodeMaster(name, projectID)

	if err == ErrorNodeStateStopped {
		glog.Errorf("node is stopped, project is %s, name is %s.", projectID, name)
		http.Error(w, "node is stopped.", http.StatusForbidden)
		return
	} else if err != nil {
		glog.Error(err.Error())
		http.Error(w, "fail to get master address", http.StatusInternalServerError)
		return
	}

	// here using a function is to reduce cyclomatic complexity
	ph.handlerHTTPResponse(url, projectID, name, rootDir, w)
}

func (ph *Handle) handlerHTTPResponse(url, projectID, name, rootDir string, w http.ResponseWriter) {
	if url == "" {
		index := ph.scheduleKubeAPIURL()
		if index < 0 {
			http.Error(w, "no available edge master", http.StatusInternalServerError)
			return
		}
		err := ph.ensureNamespace(projectID, index)
		if err != nil {
			glog.Error(err.Error())
			http.Error(w, "fail to ensure namespace", http.StatusInternalServerError)
			return
		}

		node := EdgeNode{Name: name, ProjectID: projectID, MasterAddr: ph.endpoints[index].ProxyURL, DockerRootDir: rootDir}
		err = ph.updateEdgeNodeMaster(&node)
		if err != nil {
			glog.Error(err.Error())
			http.Error(w, "fail to update master address", http.StatusInternalServerError)
			return
		}
		io.WriteString(w, ph.endpoints[index].ExternalURL)
	} else {
		endpoint := ph.getEndpointByProxyURL(url)
		if endpoint == nil {
			glog.Errorf("master address %s from edge manager not found in configuration", url)
			http.Error(w, "registered master address outdated", http.StatusInternalServerError)
			return
		}
		io.WriteString(w, endpoint.ExternalURL)
	}
}

func getUserInfoFromCert(cert *x509.Certificate) (string, string, error) {
	user := ""
	project := ""
	for _, i := range cert.Subject.Names {
		if i.Type.String() == CertUserNameKey {
			user = i.Value.(string)
		} else if i.Type.String() == CertProjectIDKey {
			project = i.Value.(string)
		}
	}
	if project == "" {
		return "", "", newUserInfoExtractError()
	}
	if !strings.HasPrefix(user, UserInfoPrefix) {
		return "", "", newUserInfoExtractError()
	}
	tokens := strings.Split(user, ":")
	if tokens[len(tokens)-1] == "" {
		return "", "", newUserInfoExtractError()
	}
	return tokens[len(tokens)-1], project, nil
}

// methods for metrics
func (ph *Handle) checkManager() int {
	url := fmt.Sprintf("%s/%s/dummy-project/edgemgr_internal", ph.edgeAPIURL, ph.edgeAPIVersion)
	resp, err := ph.edgeClient.Get(url)
	if err != nil {
		glog.Errorf("edge manager is not available, reason %s", err.Error())
		return 0
	}
	switch resp.StatusCode {
	case http.StatusOK, http.StatusNotFound, http.StatusUnauthorized:
		return 1
	default:
		glog.Errorf("edge manager is not available, response code %d", resp.StatusCode)
		return 0
	}
}

func (ph *Handle) checkMaster() float64 {
	if len(ph.kubeClients) == 0 {
		return 0
	}
	opts := api.ListOptions{}
	var normalNum float64
	for i, client := range ph.kubeClients {
		_, err := client.Nodes("").List(opts)
		if err != nil {
			url := ph.endpoints[i].InternalURL
			glog.Errorf("edge master %s is not available, reason %s", url, err.Error())
			continue
		}
		normalNum++
	}
	return normalNum / float64(len(ph.kubeClients))
}

func (ph *Handle) checkAccess() float64 {
	if len(ph.queues) == 0 {
		return 0
	}
	var normalNum float64
	for _, v := range ph.queues {
		resp, err := ph.mqClient.Get(v.AccessURL + "/workload")
		if err != nil || resp.StatusCode != http.StatusOK {
			if err != nil {
				glog.Errorf("cloud access %s is not available, reason %s", v.AccessURL, err.Error())
			} else {
				glog.Errorf("cloud access %s is not available, response code %d", v.AccessURL, resp.StatusCode)
			}
			continue
		}
		normalNum++
	}
	return normalNum / float64(len(ph.queues))
}

func (ph *Handle) syncMetrics(stop chan bool) {
	for {
		managerMetric := ph.checkManager()
		accessMetric := ph.checkAccess()
		metrics.Store("manager", managerMetric)
		metrics.Store("access", accessMetric)
		time.Sleep(time.Duration(ph.metricSyncInterval) * time.Second)
		select {
		case <-stop:
			return
		default:
		}
	}
}

type filterWriter struct{}

func (f *filterWriter) Write(p []byte) (n int, err error) {
	output := string(p)
	if strings.Contains(output, "http: TLS handshake error from") {
		return 0, nil
	}
	return os.Stderr.Write(p)
}

type InstallInfoResponse struct {
	MajorVersion int `json:"major_version"`
	MinorVersion int `json:"minor_version"`
	FixVersion int `json:"fix_version"`
	BuildVersion string `json:"build_version"`
	Package string `json:"package"`
	StartParameters map[string]interface{} `json:"start_parameters"`
}

type NodeInstallStatusPut struct {
	MajorVersion string `json:"major_version"`
	MinorVersion string `json:"minor_version"`
	FixVersion   string `json:"fix_version"`
	BuildVersion string `json:"build_version"`
	Status       string `json:"status"`
	Reason       string `json:"reason"`
}


func EdgeInstallPost(w http.ResponseWriter, r *http.Request, p httprouter.Params) {
	nodeID := p.ByName("node_id")
	installType := p.ByName("type")
	glog.Infof("Get node install info request from edged host: %s", r.Host)
	//err := ph.validateCert(r)
	//if err != nil {
	//	glog.Errorf("fail to get node install info, host: %s, error message: %s", r.Host, err.Error())
	//	http.Error(w, "", http.StatusUnauthorized)
	//	return
	//}
	//name, projectID, err := getUserInfoFromCert(r.TLS.PeerCertificates[0])
	//if err != nil {
	//	glog.Errorf("fail to get node install info, project: %s, id: %s, error message: %s", projectID, name, err.Error())
	//	http.Error(w, "", http.StatusBadRequest)
	//	return
	//}
	//projectID := "d16e6eb6cc0d49a0941df2f31285757a"
	projectID := "f91453f0f3f94c15897764935b2d61c6"
	urlEdgeMgr := fmt.Sprintf("http://192.145.62.56:8143/v1/%s/edgemgr_internal/nodes/%s/installinfo/%s", projectID, nodeID, installType)
	requestBody, err := ioutil.ReadAll(r.Body)
	if err != nil{
		glog.Errorf("")
		return
	}
	reader := bytes.NewReader(requestBody)
	req, err := http.NewRequest(http.MethodPost, urlEdgeMgr, reader)
	fmt.Println(urlEdgeMgr, string(requestBody))
	if err != nil{
		glog.Errorln("")
		return
	}
	req.Header.Set("Content-type", "application/json;charset=utf8")
	edgeMgrClient := http.Client{
		Timeout:time.Second * 30,
	}
	resp, err := edgeMgrClient.Do(req)
	if err != nil{
		glog.Errorln("")
		return
	}

	defer resp.Body.Close()
	respContent, err := ioutil.ReadAll(resp.Body)
	if err != nil{
		return
	}
	if resp.StatusCode != http.StatusOK{
		if resp.StatusCode == http.StatusInternalServerError{
			http.Error(w, "fail to update master address", http.StatusInternalServerError)
			return
		}
		w.WriteHeader(resp.StatusCode)
		body := make(map[string]interface{})
		fmt.Println(string(respContent))
		err = json.Unmarshal(respContent, &body)
		if err != nil{
			glog.Errorf("Get node install info error, %s", err)
			return
		}
		fmt.Printf("%#v\n", body)
		ret, err := json.Marshal(body)
		if err != nil{
			glog.Errorf("Get node install info error, %s", err)
			return
		}
		io.WriteString(w, string(ret))
		return
	}

	var body InstallInfoResponse
	//body := make(map[string]interface{})
	fmt.Println(string(respContent))
	err = json.Unmarshal(respContent, &body)
	if err != nil{
		glog.Errorf("Get node install info error, %s", err)
		return
	}
	fmt.Printf("%#v\n", body)
	ret, err := json.Marshal(body)
	if err != nil{
		glog.Errorf("Get node install info error, %s", err)
		return
	}
	io.WriteString(w, string(ret))
}



func EdgeInstallPutNodeStatus(w http.ResponseWriter, r *http.Request, p httprouter.Params) {
	nodeID := p.ByName("node_id")
	glog.Infof("Put node install status request from edged host: %s", r.Host)
	//err := ph.validateCert(r)
	//if err != nil {
	//	glog.Errorf("fail to get node install info, host: %s, error message: %s", r.Host, err.Error())
	//	http.Error(w, "", http.StatusUnauthorized)
	//	return
	//}
	//name, projectID, err := getUserInfoFromCert(r.TLS.PeerCertificates[0])
	//if err != nil {
	//	glog.Errorf("fail to get node install info, project: %s, id: %s, error message: %s", projectID, name, err.Error())
	//	http.Error(w, "", http.StatusBadRequest)
	//	return
	//}
	projectID := "f91453f0f3f94c15897764935b2d61c6"

	//length := r.ContentLength

	//putBody := make([]byte, length)
	//r.Body.Read(putBody)
	//putBody, err := ioutil.ReadAll(r.Body)
	//var requestBody io.Reader
	//if  != "" {
	//	requestBody = bytes.NewBufferString(putBody)
	//}
	//if err != nil{
	//	glog.Errorf("Put node install status error , %s", err)
	//	return
	//}
	//var putBodyStruct NodeInstallStatusPut
	//err := json.Unmarshal(putBody, &putBodyStruct)

	urlEdgeMgr := fmt.Sprintf("http://192.145.62.56:8143/v1/%s/edgemgr_internal/nodes/%s/installinfo/nodestatus", projectID, nodeID)
	requestBody, err := ioutil.ReadAll(r.Body)
	if err != nil{
		glog.Errorf("")
		return
	}
	reader := bytes.NewReader(requestBody)
	req, err := http.NewRequest(http.MethodPut, urlEdgeMgr, reader)
	glog.Errorf("url ; %#v , Body : %#v", urlEdgeMgr, string(requestBody))
	if err != nil{
		glog.Errorf("Put node install status error, %s", err)
		return
	}
	req.Header.Set("Content-type", "application/json;charset=utf8")
	edgeMgrClient := http.Client{
		Timeout:time.Second * 30,
	}
	resp, err := edgeMgrClient.Do(req)
	if err != nil{
		glog.Errorf("Put node install status error, %s", err)
		return
	}
	if resp.StatusCode != http.StatusOK{
		return
	}
	defer resp.Body.Close()
	respContent, err := ioutil.ReadAll(resp.Body)
	if err != nil{
		return
	}
	io.WriteString(w, string(respContent))
}


func EdgeInstallGetNodeStatus(w http.ResponseWriter, r *http.Request, p httprouter.Params) {
	nodeID := p.ByName("node_id")
	glog.Infof("Get node install status request from edged host: %s", r.Host)
	//err := ph.validateCert(r)
	//if err != nil {
	//	glog.Errorf("fail to get node install info, host: %s, error message: %s", r.Host, err.Error())
	//	http.Error(w, "", http.StatusUnauthorized)
	//	return
	//}
	//name, projectID, err := getUserInfoFromCert(r.TLS.PeerCertificates[0])
	//if err != nil {
	//	glog.Errorf("fail to get node install info, project: %s, id: %s, error message: %s", projectID, name, err.Error())
	//	http.Error(w, "", http.StatusBadRequest)
	//	return
	//}
	projectID := "f91453f0f3f94c15897764935b2d61c6"

	//length := r.ContentLength

	//putBody := make([]byte, length)
	//r.Body.Read(putBody)
	//putBody, err := ioutil.ReadAll(r.Body)
	//var requestBody io.Reader
	//if  != "" {
	//	requestBody = bytes.NewBufferString(putBody)
	//}
	//if err != nil{
	//	glog.Errorf("Put node install status error , %s", err)
	//	return
	//}
	//var putBodyStruct NodeInstallStatusPut
	//err := json.Unmarshal(putBody, &putBodyStruct)

	urlEdgeMgr := fmt.Sprintf("http://192.145.62.56:8143/v1/%s/edgemgr_internal/nodes/%s/installinfo/nodestatus", projectID, nodeID)
	req, err := http.NewRequest(http.MethodGet, urlEdgeMgr, nil)
	if err != nil{
		glog.Errorf("Put node install status error, %s", err)
		return
	}
	req.Header.Set("Content-type", "application/json;charset=utf8")
	edgeMgrClient := http.Client{
		Timeout:time.Second * 30,
	}
	resp, err := edgeMgrClient.Do(req)
	if err != nil{
		glog.Errorf("Put node install status error, %s", err)
		return
	}
	if resp.StatusCode != http.StatusOK{
		return
	}
	defer resp.Body.Close()
	respContent, err := ioutil.ReadAll(resp.Body)
	if err != nil{
		return
	}
	io.WriteString(w, string(respContent))
}


func EdgePost(w http.ResponseWriter, r *http.Request, p httprouter.Params) {
	nodeID := p.ByName("post_node_id")
	projectID := p.ByName("project_id")
	glog.Infof("Get node install info request from edged host: %s", r.Host)
	//err := ph.validateCert(r)
	//if err != nil {
	//	glog.Errorf("fail to get node install info, host: %s, error message: %s", r.Host, err.Error())
	//	http.Error(w, "", http.StatusUnauthorized)
	//	return
	//}
	//name, projectID, err := getUserInfoFromCert(r.TLS.PeerCertificates[0])
	//if err != nil {
	//	glog.Errorf("fail to get node install info, project: %s, id: %s, error message: %s", projectID, name, err.Error())
	//	http.Error(w, "", http.StatusBadRequest)
	//	return
	//}
	urlEdgeMgr := fmt.Sprintf("http://192.145.62.56:8143/v1/%s/edgemgr/nodes/%s/upgrade", projectID, nodeID)
	req, err := http.NewRequest(http.MethodPost, urlEdgeMgr, nil)
	fmt.Println(urlEdgeMgr)
	if err != nil{
		glog.Errorln("")
		return
	}
	req.Header.Set("Content-type", "application/json;charset=utf8")
	req.Header.Set("X-Auth-Token", "MIISdwYJKoZIhvcNAQcCoIISaDCCEmQCAQExDTALBglghkgBZQMEAgEwghDFBgkqhkiG9w0BBwGgghC2BIIQsnsidG9rZW4iOnsiZXhwaXJlc19hdCI6IjIwMTgtMTAtMTdUMDk6NTY6NTQuOTU2MDAwWiIsIm1ldGhvZHMiOlsicGFzc3dvcmQiXSwiY2F0YWxvZyI6W10sInJvbGVzIjpbeyJuYW1lIjoidGVfYWRtaW4iLCJpZCI6IjE5OTJjMWRmOWFkNjQxMmU5YzgzMzAzMmNkNzBjYThmIn0seyJuYW1lIjoib3BfZ2F0ZWRfYWlzX2RlZm9nIiwiaWQiOiIwIn0seyJuYW1lIjoib3BfZ2F0ZWRfZGhfbWMiLCJpZCI6IjAifSx7Im5hbWUiOiJvcF9nYXRlZF9haXNfc3VwZXJfcmVzb2x1dGlvbiIsImlkIjoiMCJ9LHsibmFtZSI6Im9wX2dhdGVkX2NvbGQiLCJpZCI6IjAifSx7Im5hbWUiOiJvcF9nYXRlZF9yZHNfbXlvcHQiLCJpZCI6IjAifSx7Im5hbWUiOiJvcF9nYXRlZF9haXNfb2NyX2JhbmtfY2FyZCIsImlkIjoiMCJ9LHsibmFtZSI6Im9wX2dhdGVkX2Nic19xYWJvdCIsImlkIjoiMCJ9LHsibmFtZSI6Im9wX2dhdGVkX25ld2RkcyIsImlkIjoiMCJ9LHsibmFtZSI6Im9wX2dhdGVkX2Nic19xaSIsImlkIjoiMCJ9LHsibmFtZSI6Im9wX2dhdGVkX2Nic19ib3RsYWIiLCJpZCI6IjAifSx7Im5hbWUiOiJvcF9nYXRlZF9lcnMiLCJpZCI6IjAifSx7Im5hbWUiOiJvcF9nYXRlZF9lY3NxdWlja2RlcGxveSIsImlkIjoiMCJ9LHsibmFtZSI6Im9wX2dhdGVkX3J0cyIsImlkIjoiMCJ9LHsibmFtZSI6Im9wX2dhdGVkX3Jkc19zcWxzZXJ2ZXIyMDE3IiwiaWQiOiIwIn0seyJuYW1lIjoib3BfZ2F0ZWRfYm1zX2hwY19oMSIsImlkIjoiMCJ9LHsibmFtZSI6Im9wX2dhdGVkX2t2bSIsImlkIjoiMCJ9LHsibmFtZSI6Im9wX2dhdGVkX3NlcnZpY2VzdGFnZV9zcHJpbmdjbG91ZCIsImlkIjoiMCJ9LHsibmFtZSI6Im9wX2dhdGVkX2Vjc19yZWN5Y2xlYmluIiwiaWQiOiIwIn0seyJuYW1lIjoib3BfZ2F0ZWRfYmNzIiwiaWQiOiIwIn0seyJuYW1lIjoib3BfZ2F0ZWRfYWktZnJzIiwiaWQiOiIwIn0seyJuYW1lIjoib3BfZ2F0ZWRfY3Nic19jbGVhcl9zbmFwc2hvdCIsImlkIjoiMCJ9LHsibmFtZSI6Im9wX2dhdGVkX2Vjc19ldDIiLCJpZCI6IjAifSx7Im5hbWUiOiJvcF9nYXRlZF90ZXN0VGFnIiwiaWQiOiIwIn0seyJuYW1lIjoib3BfZ2F0ZWRfYWFkX2ZyZWUiLCJpZCI6IjAifSx7Im5hbWUiOiJvcF9nYXRlZF9yZHNfY3JlYXRlRFJJbnMiLCJpZCI6IjAifSx7Im5hbWUiOiJvcF9nYXRlZF9ncHUiLCJpZCI6IjAifSx7Im5hbWUiOiJvcF9nYXRlZF9tdHMiLCJpZCI6IjAifSx7Im5hbWUiOiJvcF9nYXRlZF9lY3NfZ3B1X3A0IiwiaWQiOiIwIn0seyJuYW1lIjoib3BfZ2F0ZWRfcmRzaTMiLCJpZCI6IjAifSx7Im5hbWUiOiJvcF9nYXRlZF9haXNfaW1hZ2VfcmVjYXB0dXJlX2RldGVjdCIsImlkIjoiMCJ9LHsibmFtZSI6Im9wX2dhdGVkX0Z1bmN0aW9uR3JhcGgiLCJpZCI6IjAifSx7Im5hbWUiOiJvcF9nYXRlZF90YWciLCJpZCI6IjAifSx7Im5hbWUiOiJvcF9nYXRlZF9kYXMiLCJpZCI6IjAifSx7Im5hbWUiOiJvcF9nYXRlZF9lY3NfaG1lbSIsImlkIjoiMCJ9LHsibmFtZSI6Im9wX2dhdGVkX2JrbGxkIiwiaWQiOiIwIn0seyJuYW1lIjoib3BfZ2F0ZWRfY2NlX2lzdGlvIiwiaWQiOiIwIn0seyJuYW1lIjoib3BfZ2F0ZWRfZHJzIiwiaWQiOiIwIn0seyJuYW1lIjoib3BfZ2F0ZWRfYWlzX3R0cyIsImlkIjoiMCJ9LHsibmFtZSI6Im9wX2dhdGVkX3JlcyIsImlkIjoiMCJ9LHsibmFtZSI6Im9wX2dhdGVkX2Vjc19kaXNraW50ZW5zaXZlIiwiaWQiOiIwIn0seyJuYW1lIjoib3BfZ2F0ZWRfaW1hZ2VzZWFyY2giLCJpZCI6IjAifSx7Im5hbWUiOiJvcF9nYXRlZF9haXNfaW1hZ2VfdGFnZ2luZyIsImlkIjoiMCJ9LHsibmFtZSI6Im9wX2dhdGVkX2Fpc19hc3Jfc2VudGVuY2UiLCJpZCI6IjAifSx7Im5hbWUiOiJvcF9nYXRlZF9lY3MiLCJpZCI6IjAifSx7Im5hbWUiOiJvcF9nYXRlZF9jZ3MiLCJpZCI6IjAifSx7Im5hbWUiOiJvcF9nYXRlZF9zd3IiLCJpZCI6IjAifSx7Im5hbWUiOiJvcF9nYXRlZF9kZHMtaHdJbnN0YW5jZSIsImlkIjoiMCJ9LHsibmFtZSI6Im9wX2dhdGVkX2NzYnNfcmVwbGljYXRpb24iLCJpZCI6IjAifSx7Im5hbWUiOiJvcF9nYXRlZF9jc2JzX3JlcF9hY2NlbGVyYXRpb24iLCJpZCI6IjAifSx7Im5hbWUiOiJvcF9nYXRlZF9haXNfYXBpX2ltYWdlX2FudGlfYWQiLCJpZCI6IjAifSx7Im5hbWUiOiJvcF9nYXRlZF9vcF9nYXRlZF9zY2NfaHZkIiwiaWQiOiIwIn0seyJuYW1lIjoib3BfZ2F0ZWRfY2NlX3Rlc3QiLCJpZCI6IjAifSx7Im5hbWUiOiJvcF9nYXRlZF92aXBfYmFuZHdpZHRoIiwiaWQiOiIwIn0seyJuYW1lIjoib3BfZ2F0ZWRfY2NlX2dwdSIsImlkIjoiMCJ9LHsibmFtZSI6Im9wX2dhdGVkX2VkYV9tYyIsImlkIjoiMCJ9LHsibmFtZSI6Im9wX2dhdGVkX29wX2dhdGVkX3NjY19zY3MiLCJpZCI6IjAifSx7Im5hbWUiOiJvcF9nYXRlZF9haXNfb2NyX2JhcmNvZGUiLCJpZCI6IjAifSx7Im5hbWUiOiJvcF9nYXRlZF9pdnMiLCJpZCI6IjAifSx7Im5hbWUiOiJvcF9nYXRlZF90ZXN0dGVzdDAxIiwiaWQiOiIwIn0seyJuYW1lIjoib3BfZ2F0ZWRfY3JlYXRlR1JJbnMiLCJpZCI6IjAifSx7Im5hbWUiOiJvcF9nYXRlZF9kZHNfc2luZ2xlIiwiaWQiOiIwIn0seyJuYW1lIjoib3BfZ2F0ZWRfYWlzX2Rpc3RvcnRpb25fY29ycmVjdCIsImlkIjoiMCJ9LHsibmFtZSI6Im9wX2dhdGVkX2NzZSIsImlkIjoiMCJ9LHsibmFtZSI6Im9wX2dhdGVkX2Jtc19jb21tb25fczMiLCJpZCI6IjAifSx7Im5hbWUiOiJvcF9nYXRlZF9haXNfaW1hZ2VfY2xhcml0eV9kZXRlY3QiLCJpZCI6IjAifSx7Im5hbWUiOiJvcF9nYXRlZF9haXNfaW1hZ2VfYW50aV9hZCIsImlkIjoiMCJ9LHsibmFtZSI6Im9wX2dhdGVkX2ZncyIsImlkIjoiMCJ9LHsibmFtZSI6Im9wX2dhdGVkX2Rjc19kY3MyIiwiaWQiOiIwIn0seyJuYW1lIjoib3BfZ2F0ZWRfaXZhIiwiaWQiOiIwIn0seyJuYW1lIjoib3BfZ2F0ZWRfZHdzX2ZlYXR1cmUiLCJpZCI6IjAifSx7Im5hbWUiOiJvcF9sZWdhY3kiLCJpZCI6IjAifSx7Im5hbWUiOiJvcF9nYXRlZF9TTVMiLCJpZCI6IjAifSx7Im5hbWUiOiJvcF9nYXRlZF9jbW4iLCJpZCI6IjAifSx7Im5hbWUiOiJvcF9nYXRlZF9lY3Nfc3BvdCIsImlkIjoiMCJ9LHsibmFtZSI6Im9wX2dhdGVkX2Rkc19od0luc3RhbmNlIiwiaWQiOiIwIn0seyJuYW1lIjoib3BfZ2F0ZWRfeWd0IiwiaWQiOiIwIn0seyJuYW1lIjoib3BfZ2F0ZWRfZXBzIiwiaWQiOiIwIn0seyJuYW1lIjoib3BfZ2F0ZWRfZ2F0ZWRfa21zIiwiaWQiOiIwIn0seyJuYW1lIjoib3BfZ2F0ZWRfb3BfZ2F0ZWRfc2NjX3dhZiIsImlkIjoiMCJ9LHsibmFtZSI6Im9wX2dhdGVkX2Fpc19vY3JfcXJfY29kZSIsImlkIjoiMCJ9LHsibmFtZSI6Im9wX2dhdGVkX2Rjc19jbHVzdGVyIiwiaWQiOiIwIn0seyJuYW1lIjoib3BfZ2F0ZWRfc21uX2FwcCIsImlkIjoiMCJ9LHsibmFtZSI6Im9wX2dhdGVkX3Nwb3QiLCJpZCI6IjAifSx7Im5hbWUiOiJvcF9nYXRlZF9haXNfZGFya19lbmhhbmNlIiwiaWQiOiIwIn0seyJuYW1lIjoib3BfZ2F0ZWRfcmRzX2NyZWF0ZUdSSW5zIiwiaWQiOiIwIn0seyJuYW1lIjoib3BfZ2F0ZWRfb3BfZ2F0ZWRfc2NjX3B0cyIsImlkIjoiMCJ9LHsibmFtZSI6Im9wX2dhdGVkX2VsYXN0aWNzZWFyY2giLCJpZCI6IjAifSx7Im5hbWUiOiJvcF9nYXRlZF9uYXRndyIsImlkIjoiMCJ9LHsibmFtZSI6Im9wX2dhdGVkX3Jkcy10cmFuc2ZlciIsImlkIjoiMCJ9LHsibmFtZSI6Im9wX2dhdGVkX2NzZV9zZXJ2aWNlY29tYl9waHlzaWNhbCIsImlkIjoiMCJ9LHsibmFtZSI6Im9wX2dhdGVkX2RlcyIsImlkIjoiMCJ9LHsibmFtZSI6Im9wX2dhdGVkX2Nsb3VkdGVzdF9wdF9od0luc3RhbmNlIiwiaWQiOiIwIn0seyJuYW1lIjoib3BfZ2F0ZWRfYWlzX29jcl9wbGF0ZV9udW1iZXIiLCJpZCI6IjAifSx7Im5hbWUiOiJvcF9nYXRlZF9jY2Vfd2luIiwiaWQiOiIwIn1dLCJwcm9qZWN0Ijp7ImRvbWFpbiI6eyJuYW1lIjoiaWVmX3EwMDIyMjIxOSIsImlkIjoiOGZkMzY5MWQwMWY0NGJhZGFiMDNlMjhkODFhYmQ0ZmYifSwibmFtZSI6InNvdXRoY2hpbmEiLCJpZCI6ImQxNmU2ZWI2Y2MwZDQ5YTA5NDFkZjJmMzEyODU3NTdhIn0sImlzc3VlZF9hdCI6IjIwMTgtMTAtMTZUMDk6NTY6NTQuOTU2MDAwWiIsInVzZXIiOnsiZG9tYWluIjp7Im5hbWUiOiJpZWZfcTAwMjIyMjE5IiwiaWQiOiI4ZmQzNjkxZDAxZjQ0YmFkYWIwM2UyOGQ4MWFiZDRmZiJ9LCJuYW1lIjoiaWVmX3EwMDIyMjIxOSIsImlkIjoiNTVjZjBiMTM5Mjc2NGZhYjg2NzJiZWY1ZGVlMTU2ZGMifX19MYIBhTCCAYECAQEwXDBXMQswCQYDVQQGEwJVUzEOMAwGA1UECAwFVW5zZXQxDjAMBgNVBAcMBVVuc2V0MQ4wDAYDVQQKDAVVbnNldDEYMBYGA1UEAwwPd3d3LmV4YW1wbGUuY29tAgEBMAsGCWCGSAFlAwQCATANBgkqhkiG9w0BAQEFAASCAQAAkEbd6doiid1tdTyzz8A0sNfuvlSnLEyoHVEDEbgmn2dCZ84FNKpVcHl63zeHejx1rm3AUilxCtRF-DvKbzXgiyJNyRF3A5nPWLzUmQ8SXHenWqCzIlfAnhFaWwZd4tIntQtTkPVCtO-hYxI1RuDhfoOCBVl7yB2vsMssjxOSvqaxWpD2GspfLcauOmNL5Penfm3qIpRSFBSV0eVYXY7mvloYFQhx8jAbl0Yb9hg7tTd-I8tDuUjD1yA06RrV1FzTTkPs+owDSg2nzxGYhnTMpvsjXRGbocxSAD5vXa6yIHV9aMR8lwGr3uAbgWxl7iW8Un-SPKEN9KfX9ken1+Y6")
	edgeMgrClient := http.Client{
		Timeout:time.Second * 30,
	}
	resp, err := edgeMgrClient.Do(req)
	if err != nil{
		glog.Errorln("")
		return
	}
	if resp.StatusCode != http.StatusOK{
		return
	}
	defer resp.Body.Close()
	io.WriteString(w, "success")
}


// StartServer starts the placement service
func StartServer() {
	config, err := InitPlacementConfig()
	if err != nil {
		panic(err)
	}
	//ph, err := newPlacementHandle(config)
	//if err != nil {
	//	panic(err)
	//}
	//cert, err := tls.X509KeyPair(config.certData, config.keyData)
	//if err != nil {
	//	panic(err)
	//}
	//tlsConfig := tls.Config{
	//	ClientCAs:    ph.verifyOpts.Roots,
	//ClientAuth:   tls.RequestClientCert,
	//Certificates: []tls.Certificate{cert},
	//}

	if config.initDB {
		migrate(config.dbConnStr)
	}

	// never stop
	//stop := make(chan bool)
	//go ph.syncMetrics(stop)

	//mux := http.NewServeMux()
	//mux.HandleFunc("/v1/placement_external/message_queue", ph.ServeGetMQ)
	//mux.HandleFunc("/v1/placement_external/nodes/:node_id/installinfo", EdgeInstallServer)
	//httprouter.New()
	mux_new := httprouter.New()
	//mux_new.GET("/v1/placement_external/nodes/:node_id/installinfo/:type", EdgeInstallPost)
	//http://{{url}}:{{port}}/v1/{{project_id}}/edgemgr/nodes/{{post_node_id}}/upgrade
	//	mux_new.POST("/v1/:project_id/edgemgr/nodes/:post_node_id/upgrade", EdgePost)
	mux_new.POST("/v1/placement_external/nodes/:node_id/installinfo/:type", EdgeInstallPost)
	mux_new.PUT("/v1/placement_external/nodes/:node_id/installinfo/nodestatus", EdgeInstallPutNodeStatus)
	mux_new.GET("/v1/placement_external/nodes/:node_id/installinfo/nodestatus", EdgeInstallGetNodeStatus)


	s := &http.Server{
		Addr:        fmt.Sprintf("%s:%d", config.placementAddress, config.placementPort),
		Handler:     mux_new,
		//TLSConfig:   &tlsConfig,
		IdleTimeout: 30 * time.Second,
		ErrorLog:    log.New(&filterWriter{}, "", log.LstdFlags),
	}
	glog.Info("Start placement server")
	//go startMetricServer(fmt.Sprintf("%s:%d", config.placementAddress, config.metricPort))
	//go startRouterServer(config, ph)
	//s.ListenAndServeTLS("", "")
	s.ListenAndServe()
}

