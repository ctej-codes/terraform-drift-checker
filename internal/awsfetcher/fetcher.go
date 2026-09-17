package awsfetcher

import (
    "context"
    "fmt"
    "strings"
    "errors"

    "github.com/aws/aws-sdk-go-v2/config"
    "github.com/aws/aws-sdk-go-v2/service/ec2"
    "github.com/aws/aws-sdk-go-v2/service/rds"
    "github.com/aws/aws-sdk-go-v2/service/route53"
    "github.com/aws/aws-sdk-go-v2/service/s3"
    elbv2 "github.com/aws/aws-sdk-go-v2/service/elasticloadbalancingv2"
    "github.com/ctej-codes/terraform-drift-checker/internal/models"
    "github.com/ctej-codes/terraform-drift-checker/internal/diag"
    "github.com/aws/smithy-go"
)

// isS3NotConfiguredError returns true for common S3 API errors that indicate
// a configuration is simply not present on the bucket (expected 404-like cases)
func isS3NotConfiguredError(err error) bool {
    if err == nil {
        return false
    }
    var ae smithy.APIError
    if errors.As(err, &ae) {
        switch ae.ErrorCode() {
        case "NoSuchCORSConfiguration",
            "NoSuchWebsiteConfiguration",
            "ReplicationConfigurationNotFoundError",
            "NoSuchBucketPolicy",
            "NoSuchLifecycleConfiguration",
            "ServerSideEncryptionConfigurationNotFoundError",
            "NoSuchBucket",
            "NoSuchTagSet",
            "NoSuchPublicAccessBlockConfiguration":
            return true
        }
    }
    // fallback: some errors may not implement APIError; ignore common substrings
    if strings.Contains(err.Error(), "does not have a website configuration") || strings.Contains(err.Error(), "The CORS configuration does not exist") || strings.Contains(err.Error(), "replication configuration was not found") {
        return true
    }
    return false
}

// FetchAll fetches a small set of AWS resources (EC2 instances, S3 buckets, RDS instances, ELBs, Route53 zones).
// FetchForExpected fetches AWS resources only for the expected resources declared in Terraform state.
func FetchForExpected(ctx context.Context, region string, expected []models.Resource) ([]models.Resource, error) {
    cfg, err := config.LoadDefaultConfig(ctx, config.WithRegion(region))
    if err != nil {
        return nil, fmt.Errorf("loading aws config: %w", err)
    }

    var results []models.Resource

    // Build sets of expected IDs/names per resource type to allow targeted fetches
    expectByType := map[string]map[string]struct{}{}
    for _, e := range expected {
        if _, ok := expectByType[e.Type]; !ok {
            expectByType[e.Type] = map[string]struct{}{}
        }
        key := ""
        if e.ID != "" {
            key = e.ID
        } else if e.Name != "" {
            key = e.Name
        }
        // Normalize known patterns, e.g., S3 ARNs
        if e.Type == "aws_s3_bucket" && key != "" {
            // example ARN: arn:aws:s3:::bucket-name
            if strings.HasPrefix(key, "arn:aws:s3:::") {
                key = strings.TrimPrefix(key, "arn:aws:s3:::")
            }
        }
        if key != "" {
            expectByType[e.Type][key] = struct{}{}
        }
    }

    // Log expected resources for debugging
    for t, set := range expectByType {
        for k := range set {
            diag.Debug("Expect resource type=%s key=%s", t, k)
        }
    }

    // EC2 instances - targeted by Instance IDs when present
    if ids, ok := expectByType["aws_instance"]; ok && len(ids) > 0 {
        diag.Debug("Fetching EC2 instances in region %s", region)
        ec2c := ec2.NewFromConfig(cfg)
        var idList []string
        for id := range ids {
            idList = append(idList, id)
        }
        // DescribeInstances accepts up to a certain number; pass the list directly
        out, err := ec2c.DescribeInstances(ctx, &ec2.DescribeInstancesInput{InstanceIds: idList})
        if err == nil {
            for _, resv := range out.Reservations {
                for _, inst := range resv.Instances {
                    r := models.Resource{
                        Provider:   "aws",
                        Region:     region,
                        Type:       "aws_instance",
                        ID:         "",
                        Name:       "",
                        Attributes: map[string]string{},
                        Tags:       map[string]string{},
                    }
                    if inst.InstanceId != nil {
                        r.ID = *inst.InstanceId
                    }
                    if inst.InstanceType != "" {
                        r.Attributes["instance_type"] = string(inst.InstanceType)
                    }
                    if inst.State != nil && inst.State.Name != "" {
                        r.Attributes["state"] = string(inst.State.Name)
                    }
                    if inst.PrivateIpAddress != nil {
                        r.Attributes["private_ip"] = *inst.PrivateIpAddress
                    }
                    if inst.PublicIpAddress != nil {
                        r.Attributes["public_ip"] = *inst.PublicIpAddress
                    }
                    if inst.Architecture != "" {
                        r.Attributes["architecture"] = string(inst.Architecture)
                    }
                    if inst.SubnetId != nil {
                        r.Attributes["subnet_id"] = *inst.SubnetId
                    }
                    if inst.VpcId != nil {
                        r.Attributes["vpc_id"] = *inst.VpcId
                    }
                    if inst.LaunchTime != nil {
                        r.Attributes["launch_time"] = inst.LaunchTime.String()
                    }
                    for _, t := range inst.Tags {
                        if t.Key != nil && t.Value != nil {
                            r.Tags[*t.Key] = *t.Value
                        }
                    }
                    results = append(results, r)
                }
            }
        }
    }

    // S3 buckets - list buckets and filter to expected names
    if sset, ok := expectByType["aws_s3_bucket"]; ok && len(sset) > 0 {
        diag.Debug("Listing S3 buckets")
        s3c := s3.NewFromConfig(cfg)
        sb, err := s3c.ListBuckets(ctx, &s3.ListBucketsInput{})
        if err == nil {
            expectedS3 := map[string]struct{}{}
            for n := range sset {
                expectedS3[n] = struct{}{}
            }
            for _, b := range sb.Buckets {
                if b.Name == nil {
                    continue
                }
                if _, want := expectedS3[*b.Name]; !want {
                    continue
                }
                r := models.Resource{
                    Provider:   "aws",
                    Region:     "global",
                    Type:       "aws_s3_bucket",
                    ID:         *b.Name,
                    Name:       *b.Name,
                    Attributes: map[string]string{},
                    Tags:       map[string]string{},
                }
                // populate basic attributes derived from bucket name
                r.Attributes["bucket"] = *b.Name
                r.Attributes["id"] = *b.Name
                r.Attributes["arn"] = fmt.Sprintf("arn:aws:s3:::%s", *b.Name)
                r.Attributes["bucket_domain_name"] = fmt.Sprintf("%s.s3.amazonaws.com", *b.Name)
                r.Attributes["bucket_namespace"] = "global"
                if b.CreationDate != nil {
                    r.Attributes["creation_date"] = b.CreationDate.String()
                }
                // Fetch bucket tagging to populate actual tags
                // If tagging not found, skip silently
                tb, terr := s3c.GetBucketTagging(ctx, &s3.GetBucketTaggingInput{Bucket: b.Name})
                if terr != nil {
                    if !isS3NotConfiguredError(terr) {
                        diag.Debug("GetBucketTagging failed for %s: %v", *b.Name, terr)
                    }
                } else if tb != nil {
                    for _, tag := range tb.TagSet {
                        if tag.Key != nil && tag.Value != nil {
                            r.Tags[*tag.Key] = *tag.Value
                        }
                    }
                }
                // Fetch ACL
                if acl, aerr := s3c.GetBucketAcl(ctx, &s3.GetBucketAclInput{Bucket: b.Name}); aerr != nil {
                    if !isS3NotConfiguredError(aerr) {
                        diag.Debug("GetBucketAcl failed for %s: %v", *b.Name, aerr)
                    }
                } else if acl != nil {
                    // summarize owner and grants
                    if acl.Owner != nil && acl.Owner.DisplayName != nil {
                        r.Attributes["acl_owner"] = *acl.Owner.DisplayName
                    }
                    // create a simple acl summary
                    grants := ""
                    for _, g := range acl.Grants {
                        gr := ""
                        if g.Grantee != nil {
                            if g.Grantee.URI != nil {
                                gr += *g.Grantee.URI
                            }
                            if g.Grantee.ID != nil {
                                if gr != "" { gr += "," }
                                gr += *g.Grantee.ID
                            }
                        }
                        if g.Permission != "" {
                            if gr != "" { gr += ":" }
                            gr += string(g.Permission)
                        }
                        if gr != "" {
                            if grants != "" { grants += ";" }
                            grants += gr
                        }
                    }
                    if grants != "" {
                        r.Attributes["acl"] = grants
                    }
                }
                // Fetch versioning
                if vb, verr := s3c.GetBucketVersioning(ctx, &s3.GetBucketVersioningInput{Bucket: b.Name}); verr != nil {
                    if !isS3NotConfiguredError(verr) {
                        diag.Debug("GetBucketVersioning failed for %s: %v", *b.Name, verr)
                    }
                } else if vb != nil {
                    if vb.Status != "" {
                        r.Attributes["versioning"] = fmt.Sprintf("%v", vb.Status)
                    }
                }
                // Get lifecycle configuration
                if lc, lcerr := s3c.GetBucketLifecycleConfiguration(ctx, &s3.GetBucketLifecycleConfigurationInput{Bucket: b.Name}); lcerr != nil {
                    if !isS3NotConfiguredError(lcerr) {
                        diag.Debug("GetBucketLifecycleConfiguration failed for %s: %v", *b.Name, lcerr)
                    }
                } else if lc != nil {
                    if len(lc.Rules) > 0 {
                        r.Attributes["lifecycle_rules"] = fmt.Sprintf("%d", len(lc.Rules))
                    }
                }
                // Get logging
                if logOut, lgerr := s3c.GetBucketLogging(ctx, &s3.GetBucketLoggingInput{Bucket: b.Name}); lgerr != nil {
                    if !isS3NotConfiguredError(lgerr) {
                        diag.Debug("GetBucketLogging failed for %s: %v", *b.Name, lgerr)
                    }
                } else if logOut != nil && logOut.LoggingEnabled != nil {
                    if logOut.LoggingEnabled.TargetBucket != nil {
                        r.Attributes["logging_target_bucket"] = *logOut.LoggingEnabled.TargetBucket
                    }
                    if logOut.LoggingEnabled.TargetPrefix != nil {
                        r.Attributes["logging_target_prefix"] = *logOut.LoggingEnabled.TargetPrefix
                    }
                }
                // Get encryption
                if enc, enerr := s3c.GetBucketEncryption(ctx, &s3.GetBucketEncryptionInput{Bucket: b.Name}); enerr != nil {
                    if !isS3NotConfiguredError(enerr) {
                        diag.Debug("GetBucketEncryption failed for %s: %v", *b.Name, enerr)
                    }
                } else if enc != nil && enc.ServerSideEncryptionConfiguration != nil {
                    r.Attributes["server_side_encryption_configuration"] = fmt.Sprintf("%v", len(enc.ServerSideEncryptionConfiguration.Rules))
                }
                // Get CORS
                if cors, cerr := s3c.GetBucketCors(ctx, &s3.GetBucketCorsInput{Bucket: b.Name}); cerr != nil {
                    if !isS3NotConfiguredError(cerr) {
                        diag.Debug("GetBucketCors failed for %s: %v", *b.Name, cerr)
                    }
                } else if cors != nil && len(cors.CORSRules) > 0 {
                    r.Attributes["cors_rule_count"] = fmt.Sprintf("%d", len(cors.CORSRules))
                }
                // Get website config
                if web, werr := s3c.GetBucketWebsite(ctx, &s3.GetBucketWebsiteInput{Bucket: b.Name}); werr != nil {
                    if !isS3NotConfiguredError(werr) {
                        diag.Debug("GetBucketWebsite for %s: %v", *b.Name, werr)
                    }
                } else if web != nil {
                    r.Attributes["website_endpoint"] = "enabled"
                }
                // Get replication
                if rep, rerr := s3c.GetBucketReplication(ctx, &s3.GetBucketReplicationInput{Bucket: b.Name}); rerr != nil {
                    if !isS3NotConfiguredError(rerr) {
                        diag.Debug("GetBucketReplication failed for %s: %v", *b.Name, rerr)
                    }
                } else if rep != nil && rep.ReplicationConfiguration != nil && len(rep.ReplicationConfiguration.Rules) > 0 {
                    r.Attributes["replication_rules"] = fmt.Sprintf("%d", len(rep.ReplicationConfiguration.Rules))
                }
                // Fetch location (may be empty for us-east-1)
                if lb, lerr := s3c.GetBucketLocation(ctx, &s3.GetBucketLocationInput{Bucket: b.Name}); lerr != nil {
                    diag.Debug("GetBucketLocation failed for %s: %v", *b.Name, lerr)
                } else if lb != nil {
                    regionVal := ""
                    if lb.LocationConstraint != "" {
                        regionVal = fmt.Sprintf("%v", lb.LocationConstraint)
                    } else {
                        regionVal = "us-east-1"
                    }
                    r.Attributes["bucket_region"] = regionVal
                    r.Attributes["bucket_regional_domain_name"] = fmt.Sprintf("%s.s3.%s.amazonaws.com", *b.Name, regionVal)
                    r.Attributes["region"] = regionVal
                    // map region to hosted zone id when known
                    hostedZone := map[string]string{
                        "us-east-1": "Z3AQBSTGFYJSTF",
                        "us-east-2": "Z2A6R4IJ4B0G3X",
                        "us-west-1": "Z2F56UZL2M1ACD",
                        "us-west-2": "Z3BJ6K6RIION7M",
                        "eu-west-1": "Z1GKAAA0HZ3G0Z",
                        "eu-central-1": "Z21DNDUVLTQW6Q",
                        "ap-southeast-1": "Z1P8W4RBWMG6L4",
                        "ap-southeast-2": "Z1GM3OXH4ZPM65",
                        "ap-northeast-1": "Z2M4EHUR26P7ZW",
                        "ap-northeast-2": "Z3W03O7B5YMIYP",
                        "ap-south-1": "Z11RGJOFQNVJUP",
                        "sa-east-1": "Z7KQH4QJS55SO",
                        "ca-central-1": "Z1QDHH18159H29",
                    }
                    if hz, ok := hostedZone[regionVal]; ok {
                        r.Attributes["hosted_zone_id"] = hz
                    }
                    r.Attributes["bucket_regional_domain_name"] = fmt.Sprintf("%s.s3.%s.amazonaws.com", *b.Name, regionVal)
                }
                // Fetch request payer
                if rp, rperr := s3c.GetBucketRequestPayment(ctx, &s3.GetBucketRequestPaymentInput{Bucket: b.Name}); rperr != nil {
                    diag.Debug("GetBucketRequestPayment failed for %s: %v", *b.Name, rperr)
                } else if rp != nil {
                    if rp.Payer != "" {
                        r.Attributes["request_payer"] = fmt.Sprintf("%v", rp.Payer)
                    }
                }
                results = append(results, r)
            }
        }
    }

    // RDS instances
    // RDS instances - fetch by identifier when declared
    if rset, ok := expectByType["aws_db_instance"]; ok && len(rset) > 0 {
        diag.Debug("Fetching RDS instances in region %s", region)
        rdsC := rds.NewFromConfig(cfg)
        for id := range rset {
            rd, err := rdsC.DescribeDBInstances(ctx, &rds.DescribeDBInstancesInput{DBInstanceIdentifier: &id})
            if err != nil {
                // not found or other error; skip
                continue
            }
            for _, db := range rd.DBInstances {
                r := models.Resource{
                    Provider:   "aws",
                    Region:     region,
                    Type:       "aws_db_instance",
                    ID:         "",
                    Name:       "",
                    Attributes: map[string]string{},
                    Tags:       map[string]string{},
                }
                if db.DBInstanceIdentifier != nil {
                    r.ID = *db.DBInstanceIdentifier
                    r.Name = *db.DBInstanceIdentifier
                }
                if db.DBInstanceClass != nil {
                    r.Attributes["instance_class"] = *db.DBInstanceClass
                }
                if db.DBInstanceStatus != nil {
                    r.Attributes["status"] = *db.DBInstanceStatus
                }
                if db.Engine != nil {
                    r.Attributes["engine"] = *db.Engine
                }
                if db.Endpoint != nil {
                    if db.Endpoint.Address != nil {
                        r.Attributes["endpoint_address"] = *db.Endpoint.Address
                    }
                    if db.Endpoint.Port != nil {
                        r.Attributes["endpoint_port"] = fmt.Sprintf("%d", *db.Endpoint.Port)
                    }
                }
                // Fetch RDS tags if ARN available
                if db.DBInstanceArn != nil {
                    if tagsOut, terr := rdsC.ListTagsForResource(ctx, &rds.ListTagsForResourceInput{ResourceName: db.DBInstanceArn}); terr == nil {
                        for _, t := range tagsOut.TagList {
                            if t.Key != nil && t.Value != nil {
                                r.Tags[*t.Key] = *t.Value
                            }
                        }
                    }
                }
                results = append(results, r)
            }
        }
    }

    // ELBv2 (Application/Network Load Balancers)
    // ELBv2 - fetch all then filter to expected (only if expected ELBs exist)
    if eset, ok := expectByType["aws_elb"]; ok && len(eset) > 0 {
        elb := elbv2.NewFromConfig(cfg)
        lbs, err := elb.DescribeLoadBalancers(ctx, &elbv2.DescribeLoadBalancersInput{})
        if err == nil {
            expectedELB := map[string]struct{}{}
            for k := range eset {
                expectedELB[k] = struct{}{}
            }
            for _, lb := range lbs.LoadBalancers {
                id := ""
                if lb.LoadBalancerArn != nil {
                    id = *lb.LoadBalancerArn
                }
                name := ""
                if lb.LoadBalancerName != nil {
                    name = *lb.LoadBalancerName
                }
                // check match by id or name
                if id == "" && name == "" {
                    continue
                }
                if _, want := expectedELB[id]; !want {
                    if _, want2 := expectedELB[name]; !want2 {
                        continue
                    }
                }
                r := models.Resource{
                    Provider:   "aws",
                    Region:     region,
                    Type:       "aws_elb",
                    ID:         id,
                    Name:       name,
                    Attributes: map[string]string{},
                    Tags:       map[string]string{},
                }
                if lb.Scheme != "" {
                    r.Attributes["scheme"] = string(lb.Scheme)
                }
                if lb.Type != "" {
                    r.Attributes["type"] = string(lb.Type)
                }
                if lb.DNSName != nil {
                    r.Attributes["dns_name"] = *lb.DNSName
                }
                // Fetch ELB tags
                if id != "" {
                    if td, terr := elb.DescribeTags(ctx, &elbv2.DescribeTagsInput{ResourceArns: []string{id}}); terr == nil {
                        for _, tdsc := range td.TagDescriptions {
                            for _, tag := range tdsc.Tags {
                                if tag.Key != nil && tag.Value != nil {
                                    r.Tags[*tag.Key] = *tag.Value
                                }
                            }
                        }
                    }
                }
                results = append(results, r)
            }
        }
    }

    // Route53 hosted zones (only if expected)
    r53 := route53.NewFromConfig(cfg)
    if rset, ok := expectByType["aws_route53_zone"]; ok && len(rset) > 0 {
        diag.Debug("Listing Route53 hosted zones")
        hz, err := r53.ListHostedZones(ctx, &route53.ListHostedZonesInput{})
        if err == nil {
            expectedR53 := map[string]struct{}{}
            for k := range rset {
                expectedR53[k] = struct{}{}
            }
            for _, z := range hz.HostedZones {
                id := ""
                if z.Id != nil {
                    id = *z.Id
                }
                name := ""
                if z.Name != nil {
                    name = *z.Name
                }
                if id == "" && name == "" {
                    continue
                }
                if _, want := expectedR53[id]; !want {
                    if _, want2 := expectedR53[name]; !want2 {
                        continue
                    }
                }
                r := models.Resource{
                    Provider:   "aws",
                    Region:     "global",
                    Type:       "aws_route53_zone",
                    ID:         id,
                    Name:       name,
                    Attributes: map[string]string{},
                    Tags:       map[string]string{},
                }
                if z.Config != nil {
                    r.Attributes["private_zone"] = fmt.Sprintf("%t", z.Config.PrivateZone)
                }
                results = append(results, r)
            }
        }
    }

    return results, nil
}
