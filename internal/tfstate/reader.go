package tfstate

import (
    "encoding/json"
    "io/ioutil"
    "os"
    "fmt"

    "github.com/ctej-codes/terraform-drift-checker/internal/models"
    "github.com/ctej-codes/terraform-drift-checker/internal/diag"
)

// ReadLocalState reads a local terraform state file and returns normalized resources.
func ReadLocalState(path string) ([]models.Resource, error) {
    f, err := os.Open(path)
    if err != nil {
        return nil, err
    }
    defer f.Close()

    b, err := ioutil.ReadAll(f)
    if err != nil {
        return nil, err
    }

    var raw map[string]interface{}
    if err := json.Unmarshal(b, &raw); err != nil {
        return nil, err
    }

    // tfstate format varies; try to extract resources from "resources" or "modules"
    var resources []models.Resource

    if r, ok := raw["resources"]; ok {
        if arr, ok := r.([]interface{}); ok {
            diag.Debug("parsing %d resources from tfstate", len(arr))
            for _, it := range arr {
                if m, ok := it.(map[string]interface{}); ok {
                    res := models.Resource{Provider: "terraform", Attributes: map[string]string{}, Tags: map[string]string{}}
                    if t, ok := m["type"].(string); ok {
                        res.Type = t
                    }
                    if name, ok := m["name"].(string); ok {
                        res.Name = name
                    }
                    // tfstate can have either primary or instances array
                    if primary, ok := m["primary"].(map[string]interface{}); ok {
                        if id, ok := primary["id"].(string); ok {
                            res.ID = id
                        }
                        if attrs, ok := primary["attributes"].(map[string]interface{}); ok {
                            for k, v := range attrs {
                                // handle nested tags map and tags_all
                                if k == "tags" || k == "tags_all" {
                                    switch t := v.(type) {
                                    case map[string]interface{}:
                                        for tk, tv := range t {
                                            if sv, ok := tv.(string); ok {
                                                res.Tags[tk] = sv
                                            } else {
                                                res.Tags[tk] = fmt.Sprintf("%v", tv)
                                            }
                                        }
                                    case map[string]string:
                                        for tk, sv := range t {
                                            res.Tags[tk] = sv
                                        }
                                    }
                                    continue
                                }
                                // stringify attribute values for comparison
                                switch val := v.(type) {
                                case string:
                                    if len(k) > 5 && k[:5] == "tags." {
                                        tagk := k[5:]
                                        res.Tags[tagk] = val
                                    } else {
                                        res.Attributes[k] = val
                                    }
                                case bool:
                                    res.Attributes[k] = fmt.Sprintf("%v", val)
                                case float64:
                                    // numbers in tfstate are float64
                                    if val == float64(int64(val)) {
                                        res.Attributes[k] = fmt.Sprintf("%d", int64(val))
                                    } else {
                                        res.Attributes[k] = fmt.Sprintf("%v", val)
                                    }
                                case []interface{}:
                                    // handle common structured attributes like versioning
                                    if k == "versioning" && len(val) > 0 {
                                        if m, ok := val[0].(map[string]interface{}); ok {
                                            if en, ok2 := m["enabled"].(bool); ok2 {
                                                if en {
                                                    res.Attributes[k] = "Enabled"
                                                } else {
                                                    res.Attributes[k] = "Disabled"
                                                }
                                                continue
                                            }
                                        }
                                    }
                                    // fallback: marshal to JSON
                                    if jb, err := json.Marshal(val); err == nil {
                                        res.Attributes[k] = string(jb)
                                    }
                                case map[string]interface{}:
                                    if jb, err := json.Marshal(val); err == nil {
                                        res.Attributes[k] = string(jb)
                                    }
                                default:
                                    res.Attributes[k] = fmt.Sprintf("%v", val)
                                }
                            }
                        }
                        // populate ID/Name from attributes if needed
                        if res.ID == "" {
                            if v, ok := res.Attributes["id"]; ok && v != "" {
                                res.ID = v
                            } else if v, ok := res.Attributes["arn"]; ok && v != "" {
                                res.ID = v
                            } else if v, ok := res.Attributes["bucket"]; ok && v != "" {
                                res.ID = v
                            } else if v, ok := res.Attributes["name"]; ok && v != "" {
                                res.ID = v
                            }
                        }
                        if res.Name == "" {
                            if v, ok := res.Attributes["name"]; ok && v != "" {
                                res.Name = v
                            } else if v, ok := res.Attributes["bucket"]; ok && v != "" {
                                res.Name = v
                            }
                        }
                        resources = append(resources, res)
                    } else if instArr, ok := m["instances"].([]interface{}); ok {
                        // multiple instances for the resource
                        for _, inst := range instArr {
                            if im, ok := inst.(map[string]interface{}); ok {
                                r := models.Resource{Provider: "terraform", Type: res.Type, Name: res.Name, Attributes: map[string]string{}, Tags: map[string]string{}}
                                if attrs, ok := im["attributes"].(map[string]interface{}); ok {
                                    for k, v := range attrs {
                                        if k == "tags" || k == "tags_all" {
                                            switch t := v.(type) {
                                            case map[string]interface{}:
                                                for tk, tv := range t {
                                                    if sv, ok := tv.(string); ok {
                                                        r.Tags[tk] = sv
                                                    } else {
                                                        r.Tags[tk] = fmt.Sprintf("%v", tv)
                                                    }
                                                }
                                            case map[string]string:
                                                for tk, sv := range t {
                                                    r.Tags[tk] = sv
                                                }
                                            }
                                            continue
                                        }
                                        switch val := v.(type) {
                                        case string:
                                            if len(k) > 5 && k[:5] == "tags." {
                                                tagk := k[5:]
                                                r.Tags[tagk] = val
                                            } else {
                                                r.Attributes[k] = val
                                            }
                                        case bool:
                                            r.Attributes[k] = fmt.Sprintf("%v", val)
                                        case float64:
                                            if val == float64(int64(val)) {
                                                r.Attributes[k] = fmt.Sprintf("%d", int64(val))
                                            } else {
                                                r.Attributes[k] = fmt.Sprintf("%v", val)
                                            }
                                        case []interface{}:
                                            if k == "versioning" && len(val) > 0 {
                                                if m, ok := val[0].(map[string]interface{}); ok {
                                                    if en, ok2 := m["enabled"].(bool); ok2 {
                                                        if en {
                                                            r.Attributes[k] = "Enabled"
                                                        } else {
                                                            r.Attributes[k] = "Disabled"
                                                        }
                                                        continue
                                                    }
                                                }
                                            }
                                            if jb, err := json.Marshal(val); err == nil {
                                                r.Attributes[k] = string(jb)
                                            }
                                        case map[string]interface{}:
                                            if jb, err := json.Marshal(val); err == nil {
                                                r.Attributes[k] = string(jb)
                                            }
                                        default:
                                            r.Attributes[k] = fmt.Sprintf("%v", val)
                                        }
                                    }
                                }
                                // populate ID/Name
                                if r.ID == "" {
                                    if v, ok := r.Attributes["id"]; ok && v != "" {
                                        r.ID = v
                                    } else if v, ok := r.Attributes["arn"]; ok && v != "" {
                                        r.ID = v
                                    } else if v, ok := r.Attributes["bucket"]; ok && v != "" {
                                        r.ID = v
                                    } else if v, ok := r.Attributes["name"]; ok && v != "" {
                                        r.ID = v
                                    }
                                }
                                if r.Name == "" {
                                    if v, ok := r.Attributes["name"]; ok && v != "" {
                                        r.Name = v
                                    } else if v, ok := r.Attributes["bucket"]; ok && v != "" {
                                        r.Name = v
                                    }
                                }
                                resources = append(resources, r)
                            }
                        }
                    } else {
                        resources = append(resources, res)
                    }
                }
            }
        }
    }

    // If none found, return empty slice
    return resources, nil
}
