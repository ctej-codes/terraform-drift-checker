package comparator

import (
    "reflect"
    "sort"

    "github.com/ctej-codes/terraform-drift-checker/internal/diag"
    "github.com/ctej-codes/terraform-drift-checker/internal/models"
)

func Compare(expected, actual []models.Resource) []models.Diff {
    diffs := []models.Diff{}

    expMap := map[string]models.Resource{}
    actMap := map[string]models.Resource{}

    key := func(r models.Resource) string {
        id := r.ID
        if id == "" {
            id = r.Name
        }
        return r.Type + ":" + id
    }

    for _, e := range expected {
        expMap[key(e)] = e
    }
    for _, a := range actual {
        actMap[key(a)] = a
    }

    diag.Debug("comparing expected=%d actual=%d", len(expMap), len(actMap))

    // Detect Deleted & Modified
    for k, e := range expMap {
        if a, ok := actMap[k]; !ok {
            diffs = append(diffs, models.Diff{ChangeType: "Deleted", ResourceID: e.ID, Type: e.Type, Name: e.Name})
        } else {
            // Compare attributes
            changes := []models.Change{}
            // Use a curated list of keys per resource type to avoid comparing terraform-only
            // or computed attributes that driftctl can't/shouldn't fetch from cloud APIs.
            keys := compareKeysForType(e.Type)
            if len(keys) > 0 {
                for _, attr := range keys {
                    ev := e.Attributes[attr]
                    av := a.Attributes[attr]
                    if ev != av {
                        changes = append(changes, models.Change{Path: attr, Expected: ev, Actual: av})
                    }
                }
            } else {
                // default: compare all expected attributes
                for attr, ev := range e.Attributes {
                    av := a.Attributes[attr]
                    if ev != av {
                        changes = append(changes, models.Change{Path: attr, Expected: ev, Actual: av})
                    }
                }
                // detect added attributes in actual
                for attr, av := range a.Attributes {
                    if _, ok := e.Attributes[attr]; !ok {
                        changes = append(changes, models.Change{Path: attr, Expected: "", Actual: av})
                    }
                }
            }
            // tags
            if !reflect.DeepEqual(e.Tags, a.Tags) {
                changes = append(changes, models.Change{Path: "tags", Expected: formatMap(e.Tags), Actual: formatMap(a.Tags)})
            }
            if len(changes) > 0 {
                diffs = append(diffs, models.Diff{ChangeType: "Modified", ResourceID: e.ID, Type: e.Type, Name: e.Name, Changes: changes})
            }
        }
    }

    // Do not report resources that exist in cloud but are not managed by Terraform in this project.
    // (i.e., skip Added detection)

    return diffs
}

func compareKeysForType(resType string) []string {
    switch resType {
    case "aws_s3_bucket":
        return []string{"acl", "versioning", "bucket", "hosted_zone_id", "id", "bucket_region", "bucket_regional_domain_name", "region", "bucket_namespace", "bucket_domain_name", "request_payer", "arn", "acl_owner", "acl", "logging_target_bucket", "logging_target_prefix", "server_side_encryption_configuration", "cors_rule_count", "website_endpoint", "replication_rules", "lifecycle_rules"}
    case "aws_instance":
        return []string{"instance_type", "ami", "vpc_security_group_ids", "subnet_id", "monitoring"}
    case "aws_security_group":
        return []string{"description", "vpc_id", "ingress", "egress"}
    case "aws_vpc":
        return []string{"cidr_block", "enable_dns_hostnames", "enable_dns_support", "instance_tenancy"}
    case "aws_subnet":
        return []string{"cidr_block", "vpc_id", "map_public_ip_on_launch", "availability_zone"}
    default:
        return nil
    }
}

func formatMap(m map[string]string) string {
    if len(m) == 0 {
        return ""
    }
    keys := make([]string, 0, len(m))
    for k := range m {
        keys = append(keys, k)
    }
    sort.Strings(keys)
    out := ""
    for _, k := range keys {
        out += k + "=" + m[k] + ";"
    }
    return out
}
