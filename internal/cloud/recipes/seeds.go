package recipes

// BuiltinRecipes returns the 3 builtin recipes seeded by `aria-core recipe seed`.
//
// Why these three:
//   - deploy-aria-core: the running joke / canonical example (15min → 2min).
//   - backup-aria-postgres: routine ops for the cloud DB; uses vault_use.
//   - onboard-new-dev: aria-driven knowledge-transfer recipe (no shell calls).
func BuiltinRecipes() []UpsertRecipeInput {
	return []UpsertRecipeInput{
		{
			ID:                      "rec_deploy_aria_core",
			Key:                     "deploy-aria-core",
			TaskPattern:             "Deploy aria-core to production VPS",
			Stack:                   []string{"go", "templ", "postgres", "systemd"},
			Executable:              true,
			ExpectedDurationSeconds: 180,
			Steps: []Step{
				NewShellStep("git pull", "git pull --ff-only origin {{ .Vars.branch }}", "/srv/aria-core", 30),
				NewShellStep("templ generate", "go tool templ generate ./...", "/srv/aria-core", 60),
				NewShellStep("go build", "go build -o /usr/local/bin/aria-core ./cmd/aria-core", "/srv/aria-core", 180),
				NewShellStep("systemctl stop", "sudo systemctl stop aria-core", "", 10),
				NewShellStep("install binary", "sudo install -m 0755 /usr/local/bin/aria-core /opt/aria-core/aria-core", "", 10),
				NewShellStep("systemctl start", "sudo systemctl start aria-core", "", 10),
				NewShellStep("smoke check", "curl -fsS https://ariacore.itechdev.com.mx/health", "", 15),
				NewAriaSaveStep("save outcome", "Deploy aria-core completed", "deploy", "project", "aria-core", "deploy-aria-core-last",
					"Deploy {{ .Vars.branch }} succeeded.\nSmoke: {{ .Prev.Stdout }}"),
			},
		},
		{
			ID:                      "rec_backup_aria_postgres",
			Key:                     "backup-aria-postgres",
			TaskPattern:             "Backup aria-core Postgres + encrypt + record outcome",
			Stack:                   []string{"postgres", "vault"},
			Executable:              true,
			ExpectedDurationSeconds: 240,
			Steps: []Step{
				NewVaultUseStep(
					"pg_dump with vault password",
					`pg_dump "$ARIA_CORE_DATABASE_URL" | gzip > /tmp/aria-core-backup-{{ .Vars.tag }}.sql.gz`,
					[]string{"ARIA_CORE_DATABASE_URL"},
					300,
				),
				NewShellStep(
					"checksum",
					`sha256sum /tmp/aria-core-backup-{{ .Vars.tag }}.sql.gz`,
					"",
					15,
				),
				NewShellStep(
					"upload to backup dir",
					`mv /tmp/aria-core-backup-{{ .Vars.tag }}.sql.gz /var/backups/aria-core/`,
					"",
					30,
				),
				NewAriaSaveStep("save outcome", "Backup aria-postgres {{ .Vars.tag }}",
					"backup", "project", "aria-core", "backup-aria-postgres-last",
					"Backup tag={{ .Vars.tag }} completed.\nChecksum: {{ .Prev.Stdout }}"),
			},
		},
		{
			ID:                      "rec_onboard_new_dev",
			Key:                     "onboard-new-dev",
			TaskPattern:             "Onboard a new developer to a project",
			Stack:                   []string{"docs"},
			Executable:              true,
			ExpectedDurationSeconds: 60,
			Steps: []Step{
				NewPauseStep("confirm target", "Confirma proyecto target='{{ .Vars.project }}' y dev_email='{{ .Vars.dev_email }}'."),
				NewHTTPStep(
					"fetch canon obs",
					"GET",
					"{{ .Vars.aria_url }}/v1/memory/search?project={{ .Vars.project }}&type=architecture&limit=10",
					200,
					nil,
				),
				NewHTTPStep(
					"fetch skills",
					"GET",
					"{{ .Vars.aria_url }}/v1/memory/skills?stack={{ .Vars.stack }}",
					200,
					nil,
				),
				NewAriaSaveStep("save onboarding outcome",
					"Onboarding {{ .Vars.dev_email }} → {{ .Vars.project }}",
					"onboarding",
					"project",
					"{{ .Vars.project }}",
					"onboarding-{{ .Vars.dev_email }}",
					"Generated onboarding pack for {{ .Vars.dev_email }}.\nCanon and skills retrieved successfully."),
			},
		},
	}
}
