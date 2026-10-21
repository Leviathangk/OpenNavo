// desktop-publish promotes a verified GitHub release using production-local credentials.
package desktoppublish

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/opennavo/opennavo/server/internal/config"
	"github.com/opennavo/opennavo/server/internal/service/assets"
	"github.com/opennavo/opennavo/server/internal/service/desktop"
	"github.com/opennavo/opennavo/server/internal/storage"
	"github.com/opennavo/opennavo/server/internal/store"
)

func validateArtifacts(version string, raw []byte) error {
	var artifacts []struct {
		Target string `json:"target"`
		URL    string `json:"url"`
	}
	if err := json.Unmarshal(raw, &artifacts); err != nil {
		return err
	}
	expected := map[string]string{"darwin-aarch64": "OpenNavo_aarch64.app.tar.gz", "darwin-x86_64": "OpenNavo_x64.app.tar.gz", "dmg-universal": "OpenNavo_" + version + "_universal.dmg"}
	if len(artifacts) != len(expected) {
		return errors.New("release must contain all three artifacts")
	}
	for _, artifact := range artifacts {
		name, ok := expected[artifact.Target]
		if !ok || artifact.URL != "https://github.com/opennavo/OpenNavo/releases/download/desktop-v"+version+"/"+name {
			return errors.New("release artifacts must belong to the selected GitHub version")
		}
		delete(expected, artifact.Target)
	}
	return nil
}

func run(ctx context.Context, version string, apply bool) error {
	if !regexp.MustCompile(`^\d+\.\d+\.\d+(-[0-9A-Za-z.]+)?$`).MatchString(version) {
		return errors.New("invalid version")
	}
	cfg, err := config.Load()
	if err != nil {
		return errors.New("production configuration is invalid")
	}
	st, err := store.Open(ctx, cfg)
	if err != nil {
		return errors.New("cannot open production store")
	}
	defer func() { _ = st.Close() }()
	channel := "stable"
	if strings.Contains(version, "-") {
		channel = "beta"
	}
	var row store.DesktopRelease
	if err := st.DB.WithContext(ctx).Where("version = ? AND channel = ?", version, channel).First(&row).Error; err != nil {
		return errors.New("release draft not found")
	}
	if err := validateArtifacts(version, row.Artifacts); err != nil {
		return err
	}
	if !apply {
		fmt.Printf("Ready to publish %s (%s)\n", version, channel)
		return nil
	}
	client, err := storage.New(cfg)
	if err != nil {
		return errors.New("cannot initialize manifest storage")
	}
	svc := &desktop.Service{Store: st, Objects: desktop.S3Objects{S3Objects: assets.S3Objects{Client: client}}}
	if _, err := svc.Mutate(ctx, "PublishDesktopRelease", row.ID, nil, store.AuditActor{UserAgent: "OpenNavo GitHub release promotion"}); err != nil {
		return errors.New("release promotion failed; inspect production audit privately")
	}
	fmt.Printf("Published desktop %s (%s)\n", version, channel)
	return nil
}

func Main() {
	version := flag.String("version", "", "Registered GitHub release version")
	apply := flag.Bool("apply", false, "Publish the update manifest")
	flag.Parse()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	err := run(ctx, *version, *apply)
	cancel()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
