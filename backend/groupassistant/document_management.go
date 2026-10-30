package groupassistant

import "context"

type DocumentEntry struct {
	Name, SHA256 string
	Bytes        int
}
type DocumentResult struct {
	Files      []DocumentEntry `json:",omitempty"`
	NextOffset int
	Total      int
	Name       string   `json:",omitempty"`
	SHA256     string   `json:",omitempty"`
	Data       []byte   `json:",omitempty"`
	Pending    *Pending `json:",omitempty"`
}

// The local authenticated administrator uses the same ownership and fresh-file
// confirmation broker as group commands. Reads are paged to bound pipe frames.
func (s *Service) AdminDocuments(owner, group, user, action, name, confirmation, expected string, offset, limit int) (DocumentResult, error) {
	return s.AdminDocumentsContext(context.Background(), owner, group, user, action, name, confirmation, expected, offset, limit)
}

func (s *Service) AdminDocumentsContext(ctx context.Context, owner, group, user, action, name, confirmation, expected string, offset, limit int, configurationVersion ...string) (DocumentResult, error) {
	if owner != s.store.admin || !validIdentity(Identity{Group: group, User: owner}) || offset < 0 {
		return DocumentResult{}, ErrDenied
	}
	if limit == 0 {
		limit = 50
	}
	if limit < 1 || limit > 100 {
		return DocumentResult{}, ErrInvalid
	}
	s.accessMu.RLock()
	defer s.accessMu.RUnlock()
	id := Identity{Group: group, User: owner}
	version := ""
	if len(configurationVersion) != 0 {
		version = configurationVersion[0]
	}
	switch action {
	case "documents":
		return s.store.adminDocumentList(ctx, id, user, offset, limit)
	case "document-read":
		out, err := s.store.adminDocumentRead(ctx, id, user, name, offset)
		if err != nil {
			return DocumentResult{}, err
		}
		if expected != "" && expected != out.SHA256 {
			return DocumentResult{}, ErrChanged
		}
		return out, nil
	case "document-delete-preview":
		pending, err := s.store.stageAdminDelete(ctx, id, FileRef{Owner: user, Name: name}, version)
		return DocumentResult{Pending: pending}, err
	case "document-delete":
		if confirmation == "" {
			return DocumentResult{}, ErrInvalid
		}
		return DocumentResult{}, s.store.confirmAdminDelete(ctx, id, user, name, confirmation, version)
	default:
		return DocumentResult{}, ErrInvalid
	}
}
