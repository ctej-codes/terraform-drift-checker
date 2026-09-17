package models

type Resource struct {
    Provider   string            `json:"provider"`
    AccountID  string            `json:"account_id,omitempty"`
    Region     string            `json:"region,omitempty"`
    Type       string            `json:"resource_type"`
    ID         string            `json:"resource_id"`
    Name       string            `json:"name,omitempty"`
    Attributes map[string]string `json:"attributes,omitempty"`
    Tags       map[string]string `json:"tags,omitempty"`
    TfAddress  string            `json:"tf_address,omitempty"`
}

type Change struct {
    Path     string `json:"path"`
    Expected string `json:"expected,omitempty"`
    Actual   string `json:"actual,omitempty"`
}

type Diff struct {
    ChangeType string   `json:"change_type"` // Added, Deleted, Modified, TagChanged
    ResourceID string   `json:"resource_id"`
    Type       string   `json:"resource_type"`
    Name       string   `json:"name,omitempty"`
    Changes    []Change `json:"changes,omitempty"`
}
