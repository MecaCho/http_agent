package install_agent_cert

import (
	"crypto/x509"
	"reflect"
	"testing"
	"net/http"
	//"errors"
	"fmt"
	"io/ioutil"
	"strings"
	"monkey"
	"time"
	"httprouter"
	//"net/http/httptest"
)

func TestStartServer(t *testing.T) {
	fakeCA := ""
	pool := x509.NewCertPool()
	pool.AppendCertsFromPEM([]byte(fakeCA))
	opts := x509.VerifyOptions{
		Roots: pool,
		KeyUsages: []x509.ExtKeyUsage{
			x509.ExtKeyUsageServerAuth,
			x509.ExtKeyUsageClientAuth},
	}
	edgeMgrClient := http.Client{
		Timeout: time.Second * 30,
	}

	ph := EMClient{
		verifyOpts: opts,
		edgeAPIURL: "fake_url",
		edgeAPIVersion: "v2",
		edgeClient: edgeMgrClient,
	}

	// request to edgemgr error
	defer monkey.UnpatchInstanceMethod(reflect.TypeOf(&ph.edgeClient), "Get")
	monkey.PatchInstanceMethod(reflect.TypeOf(&ph.edgeClient), "Get",
		func(_ *http.Client, _ string) (*http.Response, error) {
			body := "{\"state\":\"UPGRADING\"}"
			resp := http.Response{StatusCode: http.StatusOK, Body: ioutil.NopCloser(strings.NewReader(body))}
			return &resp, nil
		})
	//p := httprouter.Params{}


	testMux := httprouter.New()
	testMux.GET("/v1/placement_external/nodes/:node_id/installinfo/nodestatus", ph.CheckAuthorition(LogUpgrade(ph.installerGetNodeSatus)))
	//w := httptest.NewRecorder()
	//
	//req := httptest.NewRequest("GET", "/v1/placement_external/nodes/:node_id/installinfo/nodestatus", nil)
	//ag.EdgeInstallGetNodeStatus(w, req, p)

	resp, err := http.Get("/v1/placement_external/nodes/:node_id/installinfo/nodestatus")
	if err != nil {
		fmt.Println(err.Error())
	}
	body, _ := ioutil.ReadAll(resp.Body)

	fmt.Println(resp.StatusCode)
	fmt.Println(resp.Header.Get("Content-Type"))
	fmt.Println(string(body))

}


//func TestAgent(t *testing.T) {
//	// var defined here
//	name := "fakeName"
//	project := "fakeProject"
//
//	accessURL := "access_url"
//	mqURLForEdged := "edged_url"
//	mqURLForManager := "manager_url"
//
//	pool := x509.NewCertPool()
//	pool.AppendCertsFromPEM([]byte(fakeCA))
//	opts := x509.VerifyOptions{
//		Roots: pool,
//		KeyUsages: []x509.ExtKeyUsage{
//			x509.ExtKeyUsageServerAuth,
//			x509.ExtKeyUsageClientAuth},
//	}
//
//
//	ph := Handle{
//		verifyOpts: opts,
//		edgeAPIURL: "fake_url",
//	}
//
//	// request to edgemgr error
//	defer monkey.UnpatchInstanceMethod(reflect.TypeOf(&ph.mqClient), "Get")
//	monkey.PatchInstanceMethod(reflect.TypeOf(&ph.mqClient), "Get",
//		func(_ *http.Client, _ string) (resp *http.Response, err error) {
//			return nil, errors.New("mqclient return error")
//		})
//
//	url, _ := ph.QueryAssignedMQ(name, project)
//	AssertStringEqual(t, "", url, "QueryAssignedMQ return is not match")
//
//	// request response OK
//	defer monkey.UnpatchInstanceMethod(reflect.TypeOf(&ph.mqClient), "Get")
//	monkey.PatchInstanceMethod(reflect.TypeOf(&ph.mqClient), "Get",
//		func(_ *http.Client, url string) (*http.Response, error) {
//			body := fmt.Sprintf("{\"mq_url\":\"%s\"}", mqURLForManager)
//			resp := http.Response{StatusCode: http.StatusOK, Body: ioutil.NopCloser(strings.NewReader(body))}
//			return &resp, nil
//		})
//
//	url, _ = ph.QueryAssignedMQ(name, project)
//	AssertStringEqual(t, mqURLForManager, url, "QueryAssignedMQ return is not match")
//
//	// not 200 returned from edgemgr
//	defer monkey.UnpatchInstanceMethod(reflect.TypeOf(&ph.mqClient), "Get")
//	monkey.PatchInstanceMethod(reflect.TypeOf(&ph.mqClient), "Get",
//		func(_ *http.Client, url string) (*http.Response, error) {
//			body := fmt.Sprintf("{\"mq_url\":\"%s\"}", mqURLForManager)
//			resp := http.Response{StatusCode: http.StatusBadRequest, Body: ioutil.NopCloser(strings.NewReader(body))}
//			return &resp, nil
//		})
//
//	url, _ = ph.QueryAssignedMQ(name, project)
//	AssertStringEqual(t, "", url, "QueryAssignedMQ return is not match")
//}

