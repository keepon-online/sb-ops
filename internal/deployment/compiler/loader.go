package compiler

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	serviceargo "github.com/keepon-online/sb-ops/internal/service/argo"
	servicecertificate "github.com/keepon-online/sb-ops/internal/service/certificate"
	servicepolicy "github.com/keepon-online/sb-ops/internal/service/policy"
	serviceprotocol "github.com/keepon-online/sb-ops/internal/service/protocol"
	"github.com/keepon-online/sb-ops/internal/service/secret"
	servicesingbox "github.com/keepon-online/sb-ops/internal/service/singbox"
	serviceuser "github.com/keepon-online/sb-ops/internal/service/user"
)

// Deployment carries the deployment parameters subscription compilation needs:
// the public address clients dial and optional external TLS certificate paths.
type Deployment struct {
	PublicAddress       string
	TLSCertPath         string
	TLSKeyPath          string
	CacheFilePath       string
	AllowPrivateAddress bool
}

func LoadInput(ctx context.Context, db *sql.DB, deployment Deployment, certificateDirs ...string) (Input, error) {
	certificateDir := ""
	if len(certificateDirs) > 0 {
		certificateDir = strings.TrimSpace(certificateDirs[0])
	}
	if strings.TrimSpace(deployment.PublicAddress) == "" {
		return Input{}, ErrPublicAddressNotConfigured
	}
	protocols, err := serviceprotocol.NewStore(db, secret.NewStore(db)).List(ctx)
	if err != nil {
		return Input{}, err
	}
	users, err := serviceuser.NewStore(db).ListArtifactUsers(ctx)
	if err != nil {
		return Input{}, err
	}
	devices, err := serviceuser.NewStore(db).ListArtifactDevices(ctx)
	if err != nil {
		return Input{}, err
	}
	assignments := map[string]map[string]bool{}
	rows, err := db.QueryContext(ctx, `SELECT user_id,protocol_id FROM user_protocols ORDER BY user_id,protocol_id`)
	if err != nil {
		return Input{}, err
	}
	defer rows.Close()
	for rows.Next() {
		var userID, protocolID string
		if err := rows.Scan(&userID, &protocolID); err != nil {
			return Input{}, err
		}
		if assignments[userID] == nil {
			assignments[userID] = map[string]bool{}
		}
		assignments[userID][protocolID] = true
	}
	if err := rows.Err(); err != nil {
		return Input{}, err
	}
	policyStore := servicepolicy.NewStore(db)
	policies, err := policyStore.ListPolicies(ctx)
	if err != nil {
		return Input{}, err
	}
	policyRules := make(map[string][]servicepolicy.Rule, len(policies))
	for _, policy := range policies {
		rules, err := policyStore.ListRules(ctx, policy.ID)
		if err != nil {
			return Input{}, err
		}
		policyRules[policy.ID] = rules
	}
	userPolicies := map[string]map[string]bool{}
	policyRows, err := db.QueryContext(ctx, `SELECT user_id,policy_id FROM user_policies ORDER BY user_id,policy_id`)
	if err != nil {
		return Input{}, err
	}
	defer policyRows.Close()
	for policyRows.Next() {
		var userID, policyID string
		if err := policyRows.Scan(&userID, &policyID); err != nil {
			return Input{}, err
		}
		if userPolicies[userID] == nil {
			userPolicies[userID] = map[string]bool{}
		}
		userPolicies[userID][policyID] = true
	}
	if err := policyRows.Err(); err != nil {
		return Input{}, err
	}
	certificatePaths, err := writeCertificatePaths(ctx, db, certificateDir, protocols)
	if err != nil {
		return Input{}, err
	}
	singBoxStore := servicesingbox.NewStore(db, secret.NewStore(db))
	singBoxService := servicesingbox.NewService(singBoxStore)
	singBoxConfig, err := singBoxService.GetConfig(ctx)
	if err != nil {
		return Input{}, err
	}
	outbounds, passwords, err := singBoxStore.CompilerOutbounds(ctx)
	if err != nil {
		return Input{}, err
	}
	routeRules, err := singBoxStore.ListRules(ctx)
	if err != nil {
		return Input{}, err
	}
	ruleSets, err := singBoxStore.ListRuleSets(ctx)
	if err != nil {
		return Input{}, err
	}
	clashSecret, err := singBoxService.ClashAPISecret(ctx)
	if err != nil {
		return Input{}, err
	}
	argoTunnels, err := serviceargo.NewStore(db, secret.NewStore(db)).List(ctx)
	if err != nil {
		return Input{}, err
	}
	return Input{
		PublicAddress: deployment.PublicAddress, TLSCertPath: deployment.TLSCertPath,
		TLSKeyPath: deployment.TLSKeyPath, CacheFilePath: deployment.CacheFilePath, AllowPrivateAddress: deployment.AllowPrivateAddress, CertificatePaths: certificatePaths, Protocols: protocols, Users: users, Devices: devices, UserProtocols: assignments,
		Policies: policies, PolicyRules: policyRules, UserPolicies: userPolicies,
		SingBoxConfig: singBoxConfig, ClashAPISecret: clashSecret, Outbounds: outbounds, OutboundPasswords: passwords, RouteRules: routeRules, RuleSets: ruleSets, ArgoTunnels: argoTunnels,
	}, nil
}

func writeCertificatePaths(ctx context.Context, db *sql.DB, certificateDir string, protocols []serviceprotocol.Protocol) (map[string]CertificatePaths, error) {
	if certificateDir == "" {
		return nil, nil
	}
	ids := map[string]bool{}
	for _, protocol := range protocols {
		if protocol.CertificateID != nil && strings.TrimSpace(*protocol.CertificateID) != "" {
			ids[*protocol.CertificateID] = true
		}
	}
	service := servicecertificate.NewService(servicecertificate.NewStore(db), secret.NewStore(db))
	paths := make(map[string]CertificatePaths, len(ids))
	for id := range ids {
		certificate, err := service.Get(ctx, id)
		if err != nil {
			return nil, err
		}
		material, err := service.Material(ctx, id)
		if err != nil {
			return nil, err
		}
		dir := filepath.Join(certificateDir, id, strconv.FormatInt(certificate.Revision, 10))
		certificatePath := filepath.Join(dir, "cert.pem")
		keyPath := filepath.Join(dir, "key.pem")
		if err := writeMaterial(certificatePath, []byte(material.CertificatePEM), 0o644); err != nil {
			return nil, err
		}
		if err := writeMaterial(keyPath, []byte(material.PrivateKeyPEM), 0o600); err != nil {
			return nil, err
		}
		paths[id] = CertificatePaths{CertificatePath: certificatePath, KeyPath: keyPath}
	}
	return paths, nil
}

func writeMaterial(path string, content []byte, mode os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return err
	}
	return os.WriteFile(path, content, mode)
}
