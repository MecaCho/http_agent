func TestAgent(t *testing.T) {
	// var defined here
	name := "fakeName"
	project := "fakeProject"

	accessURL := "access_url"
	mqURLForEdged := "edged_url"
	mqURLForManager := "manager_url"

	pool := x509.NewCertPool()
	pool.AppendCertsFromPEM([]byte(fakeCA))
	opts := x509.VerifyOptions{
		Roots: pool,
		KeyUsages: []x509.ExtKeyUsage{
			x509.ExtKeyUsageServerAuth,
			x509.ExtKeyUsageClientAuth},
	}

	qep := &QueueEndpoint{
		Index:           0,
		AccessURL:       accessURL,
		MQUrlForEdged:   mqURLForEdged,
		MQUrlForManager: mqURLForManager,
	}

	ph := Handle{
		verifyOpts: opts,
		edgeAPIURL: "fake_url",
		mqClient:   http.Client{},
		queues:     []*QueueEndpoint{qep},
	}

	// request to edgemgr error
	defer monkey.UnpatchInstanceMethod(reflect.TypeOf(&ph.mqClient), "Get")
	monkey.PatchInstanceMethod(reflect.TypeOf(&ph.mqClient), "Get",
		func(_ *http.Client, _ string) (resp *http.Response, err error) {
			return nil, errors.New("mqclient return error")
		})

	url, _ := ph.QueryAssignedMQ(name, project)
	AssertStringEqual(t, "", url, "QueryAssignedMQ return is not match")

	// request response OK
	defer monkey.UnpatchInstanceMethod(reflect.TypeOf(&ph.mqClient), "Get")
	monkey.PatchInstanceMethod(reflect.TypeOf(&ph.mqClient), "Get",
		func(_ *http.Client, url string) (*http.Response, error) {
			body := fmt.Sprintf("{\"mq_url\":\"%s\"}", mqURLForManager)
			resp := http.Response{StatusCode: http.StatusOK, Body: ioutil.NopCloser(strings.NewReader(body))}
			return &resp, nil
		})

	url, _ = ph.QueryAssignedMQ(name, project)
	AssertStringEqual(t, mqURLForManager, url, "QueryAssignedMQ return is not match")

	// not 200 returned from edgemgr
	defer monkey.UnpatchInstanceMethod(reflect.TypeOf(&ph.mqClient), "Get")
	monkey.PatchInstanceMethod(reflect.TypeOf(&ph.mqClient), "Get",
		func(_ *http.Client, url string) (*http.Response, error) {
			body := fmt.Sprintf("{\"mq_url\":\"%s\"}", mqURLForManager)
			resp := http.Response{StatusCode: http.StatusBadRequest, Body: ioutil.NopCloser(strings.NewReader(body))}
			return &resp, nil
		})

	url, _ = ph.QueryAssignedMQ(name, project)
	AssertStringEqual(t, "", url, "QueryAssignedMQ return is not match")
}
