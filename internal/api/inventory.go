package api

import (
	"net/http"
	"slices"
	"strconv"

	"github.com/wentf9/xops-mcp/internal/adapters/xops"
	"github.com/wentf9/xops-mcp/internal/service"
	"github.com/wentf9/xops-mcp/internal/storage"
	cryptoSSH "golang.org/x/crypto/ssh"
)

type hostDTO struct {
	ID string `json:"id"`
	service.HostInput
	HostKey     string `json:"hostKey"`
	Fingerprint string `json:"fingerprint"`
}
type identityDTO struct {
	ID string `json:"id"`
	service.IdentityInput
}
type nodeDTO struct {
	ID string `json:"id"`
	service.NodeInput
	Status string `json:"status"`
}
type credentialDTO struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Kind string `json:"kind"`
}
type tagDTO struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Count int    `json:"count"`
}
type inventoryDTO struct {
	Revision    uint64              `json:"revision"`
	Pending     bool                `json:"pending"`
	Hosts       []hostDTO           `json:"hosts"`
	Identities  []identityDTO       `json:"identities"`
	Nodes       []nodeDTO           `json:"nodes"`
	Credentials []credentialDTO     `json:"credentials"`
	Tags        []tagDTO            `json:"tags"`
	Policy      service.PolicyInput `json:"policy"`
}

func inventoryView(v storage.Inventory, pending bool) (inventoryDTO, error) {
	view := inventoryDTO{Revision: v.Revision, Pending: pending, Hosts: []hostDTO{}, Identities: []identityDTO{}, Nodes: []nodeDTO{}, Credentials: []credentialDTO{}, Tags: []tagDTO{}}
	snapshot, _, err := xops.Snapshot(v)
	if err != nil {
		return view, err
	}
	for id, h := range v.Hosts {
		host := hostDTO{ID: id, HostInput: service.HostInput{Name: h.Name, Address: h.Address, Port: h.Port}, HostKey: h.HostKey}
		if h.HostKey != "" {
			key, err := xops.ParseHostKey(h.HostKey)
			if err != nil {
				return view, err
			}
			host.Fingerprint = cryptoSSH.FingerprintSHA256(key)
		}
		view.Hosts = append(view.Hosts, host)
	}
	for id, i := range v.Identities {
		view.Identities = append(view.Identities, identityDTO{ID: id, IdentityInput: service.IdentityInput{Name: i.Name, User: i.User, CredentialID: i.CredentialID}})
	}
	for id, c := range v.Credentials {
		view.Credentials = append(view.Credentials, credentialDTO{ID: id, Name: c.Name, Kind: c.Kind})
	}
	tags := map[string]int{}
	for id, n := range v.Nodes {
		status := "ready"
		if n.Disabled {
			status = "disabled"
		} else {
			for _, hop := range snapshot.Targets[id].Plan.Hops {
				if v.Nodes[hop.NodeID].Disabled {
					status = "blocked"
				}
			}
		}
		view.Nodes = append(view.Nodes, nodeDTO{ID: id, NodeInput: service.NodeInput{Name: n.Name, HostID: n.HostID, IdentityID: n.IdentityID, JumpIDs: n.JumpIDs, Aliases: n.Aliases, TagIDs: n.TagIDs, Disabled: n.Disabled, SudoMode: n.SudoMode, PrivilegeCredentialID: n.PrivilegeCredentialID}, Status: status})
		for _, tag := range n.TagIDs {
			tags[tag]++
		}
	}
	for id, tag := range v.Tags {
		view.Tags = append(view.Tags, tagDTO{ID: id, Name: tag.Name, Count: tags[id]})
	}
	view.Policy = service.PolicyInput{Enabled: v.Policy.Enabled, ApprovalThreshold: v.Policy.ApprovalThreshold, NoElicitFallback: v.Policy.NoElicitFallback, BlockedPatterns: v.Policy.BlockedPatterns, ProtectedPaths: v.Policy.ProtectedPaths, Nodes: map[string]string{}}
	for id, rule := range v.Policy.NodeOverrides {
		view.Policy.Nodes[id] = rule.ApprovalThreshold
	}
	slices.SortFunc(view.Hosts, func(a, b hostDTO) int { return compare(a.Name, b.Name) })
	slices.SortFunc(view.Identities, func(a, b identityDTO) int { return compare(a.Name, b.Name) })
	slices.SortFunc(view.Nodes, func(a, b nodeDTO) int { return compare(a.Name, b.Name) })
	slices.SortFunc(view.Credentials, func(a, b credentialDTO) int { return compare(a.Name, b.Name) })
	slices.SortFunc(view.Tags, func(a, b tagDTO) int { return compare(a.Name, b.Name) })
	return view, nil
}
func compare(a, b string) int {
	if a < b {
		return -1
	}
	if a > b {
		return 1
	}
	return 0
}
func (s *Server) inventory(w http.ResponseWriter, r *http.Request) {
	v, err := s.editor.Service.Inventory(r.Context())
	if err != nil {
		failure(w, err)
		return
	}
	view, err := inventoryView(v, s.editor.Service.Coordinator.Pending())
	if err != nil {
		failure(w, err)
		return
	}
	w.Header().Set("ETag", etag(v.Revision))
	respond(w, 200, view)
}
func (s *Server) save(w http.ResponseWriter, r *http.Request, kind string) {
	rev, ok := expected(w, r)
	if !ok {
		return
	}
	id := r.PathValue("id")
	var result service.EditResult
	var err error
	switch kind {
	case "tags":
		var input service.TagInput
		if !decode(w, r, &input) {
			return
		}
		result, err = s.editor.SaveTag(r.Context(), rev, id, input)
	case "hosts":
		var input service.HostInput
		if !decode(w, r, &input) {
			return
		}
		result, err = s.editor.SaveHost(r.Context(), rev, id, input)
	case "identities":
		var input service.IdentityInput
		if !decode(w, r, &input) {
			return
		}
		result, err = s.editor.SaveIdentity(r.Context(), rev, id, input)
	case "nodes":
		var input service.NodeInput
		if !decode(w, r, &input) {
			return
		}
		result, err = s.editor.SaveNode(r.Context(), rev, id, input)
	case "credentials":
		var input service.CredentialInput
		if !decode(w, r, &input) {
			return
		}
		result, err = s.editor.SaveCredential(r.Context(), rev, id, input)
	}
	finish(w, result, err)
}
func (s *Server) remove(w http.ResponseWriter, r *http.Request, kind string) {
	rev, ok := expected(w, r)
	if !ok {
		return
	}
	result, err := s.editor.Delete(r.Context(), rev, kind, r.PathValue("id"))
	finish(w, result, err)
}
func (s *Server) policy(w http.ResponseWriter, r *http.Request) {
	rev, ok := expected(w, r)
	if !ok {
		return
	}
	var input service.PolicyInput
	if !decode(w, r, &input) {
		return
	}
	result, err := s.editor.SavePolicy(r.Context(), rev, input)
	finish(w, result, err)
}
func (s *Server) reconcile(w http.ResponseWriter, r *http.Request) {
	rev, ok := expected(w, r)
	if !ok {
		return
	}
	v, err := s.store.Load(r.Context())
	if err != nil {
		failure(w, err)
		return
	}
	if v.Revision != rev {
		failure(w, storage.ErrConflict)
		return
	}
	if err := s.editor.Service.Reconcile(r.Context()); err != nil {
		failure(w, err)
		return
	}
	finish(w, service.EditResult{Revision: rev}, nil)
}
func (s *Server) audit(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	limit := 50
	before := int64(0)
	var err error
	if raw := q.Get("limit"); raw != "" {
		limit, err = strconv.Atoi(raw)
		if err != nil {
			problem(w, 400, "invalid_query", "分页参数不正确")
			return
		}
	}
	if raw := q.Get("before"); raw != "" {
		before, err = strconv.ParseInt(raw, 10, 64)
		if err != nil {
			problem(w, 400, "invalid_query", "分页参数不正确")
			return
		}
	}
	if limit < 1 || limit > 100 || before < 0 {
		problem(w, 400, "invalid_query", "分页参数不正确")
		return
	}
	entries, err := s.store.Audit(r.Context(), storage.AuditQuery{Limit: limit, BeforeID: before, NodeID: q.Get("nodeID"), Outcome: q.Get("outcome"), OperationID: q.Get("operationID")})
	if err != nil {
		failure(w, err)
		return
	}
	next := int64(0)
	if len(entries) == limit {
		next = entries[len(entries)-1].ID
	}
	respond(w, 200, map[string]any{"entries": entries, "next": next})
}
