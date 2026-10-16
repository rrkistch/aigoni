package server

func (s *Server) wikiReady() bool {
	p, _, err := s.selectedAI()
	return err == nil && p.Key != "" && p.BaseURL != "" && p.Model != ""
}
