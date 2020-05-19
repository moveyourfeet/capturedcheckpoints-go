package main

type CapturedCheckpoint struct {
	CapturedCheckpoint string `json:"capturedcheckpoint"`
}

func NewRace(ID string) Race {
	return Race{
		ID:                  ID,
		CapturedCheckpoints: []string{},
	}
}

type Race struct {
	ID                  string   `json:"id" bson:"Id,omitempty"`
	CapturedCheckpoints []string `json:"capturedcheckpoints"`
}
