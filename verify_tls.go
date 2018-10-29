package node_install_agent

//func GetUserInfoFromCert(cert *x509.Certificate) (string, string, error) {
//	user := ""
//	project := ""
//	for _, i := range cert.Subject.Names {
//		if i.Type.String() == CertUserNameKey {
//			user = i.Value.(string)
//		} else if i.Type.String() == CertProjectIDKey {
//			project = i.Value.(string)
//		}
//	}
//	if project == "" {
//		return "", "", newUserInfoExtractError()
//	}
//	if !strings.HasPrefix(user, UserInfoPrefix) {
//		return "", "", newUserInfoExtractError()
//	}
//	tokens := strings.Split(user, ":")
//	if tokens[len(tokens)-1] == "" {
//		return "", "", newUserInfoExtractError()
//	}
//	return tokens[len(tokens)-1], project, nil
//}

//func (ph *InstallAgent) ValidateCert(r *http.Request) error {
//	if r.TLS == nil || len(r.TLS.PeerCertificates) == 0 {
//		return fmt.Errorf("no tls certificate provided")
//	}
//	_, err := r.TLS.PeerCertificates[0].Verify(ph.verifyOpts)
//	return err
//}
