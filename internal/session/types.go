package session

import "time"

type Entry struct {
	ID        string     `json:"id"`
	Timestamp time.Time  `json:"timestamp"`
	Request   ReqRecord  `json:"request"`
	Response  RespRecord `json:"response"`
}

type ReqRecord struct {
	Method   string              `json:"method"`
	URL      string              `json:"url"`
	Host     string              `json:"host"`
	Headers  map[string][]string `json:"headers"`
	BodyFile string              `json:"bodyFile,omitempty"`
	BodySize int64               `json:"bodySize,omitempty"`
}

type RespRecord struct {
	Status   int                 `json:"status"`
	Headers  map[string][]string `json:"headers"`
	BodyFile string              `json:"bodyFile,omitempty"`
	BodySize int64               `json:"bodySize,omitempty"`
}
