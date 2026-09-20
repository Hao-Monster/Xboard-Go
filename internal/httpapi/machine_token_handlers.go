package httpapi

import "net/http"

func (s *server) getMachineToken(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	id, ok := pathID(w, r, "machineID")
	if !ok {
		return
	}
	token, err := s.store.MachineToken(r.Context(), id, s.settingsCipher)
	if err != nil {
		handleStoreError(w, err)
		return
	}
	writeSuccess(w, http.StatusOK, map[string]any{"token": token, "available": token != ""})
}

func (s *server) resetMachineToken(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	id, ok := pathID(w, r, "machineID")
	if !ok {
		return
	}
	token, err := s.store.ResetMachineToken(r.Context(), id, s.settingsCipher, s.now())
	if err != nil {
		handleStoreError(w, err)
		return
	}
	if s.hub != nil {
		s.hub.DisconnectMachine(id, "machine token reset")
	}
	writeSuccess(w, http.StatusOK, map[string]any{"token": token, "available": true})
}
