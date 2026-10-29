%{!?nli_version:%global nli_version 20.0.0}
%{!?nli_release:%global nli_release 1}
Name:           nli-controller-agent
Version:        %{nli_version}
Release:        %{nli_release}%{?dist}
Summary:        Nexora Log Intelligence Ver20 controller and Web console
License:        LicenseRef-Proprietary
URL:            https://github.com/vjay136/nexora_log_intelligence_ver20.git
Source0:        %{name}-%{version}.tar.gz
ExclusiveArch:  x86_64
Requires(pre):  shadow-utils
Requires(post): systemd coreutils sed grep
Requires(preun): systemd
Requires(postun): systemd
Requires:       python3 curl openssl hostname iproute
AutoReqProv:    no
%global debug_package %{nil}
%global _build_id_links none
%global __os_install_post %{nil}

%description
NLI v20 controller providing authenticated enrollment, fleet synchronization,
rollouts, telemetry APIs, and the byte-verified webview_ver20 console.

%prep
%autosetup -n %{name}-%{version}

%build
# The static binary and Web assets are built and verified before rpmbuild.

%install
install -d %{buildroot}%{_bindir} %{buildroot}%{_sbindir} %{buildroot}%{_sysconfdir}/nli
install -d %{buildroot}/usr/lib/systemd/system %{buildroot}%{_datadir}/nli/webview_ver20 %{buildroot}%{_datadir}/nli/supabase
install -d %{buildroot}%{_datadir}/nli/metrics
install -d %{buildroot}%{_docdir}/%{name} %{buildroot}/var/lib/nli-controller
install -m 0755 bin/nli-controller-agent bin/nli %{buildroot}%{_bindir}/
install -m 0755 sbin/nli-controller-setup %{buildroot}%{_sbindir}/nli-controller-setup
install -m 0755 sbin/nli-supabase-setup %{buildroot}%{_sbindir}/nli-supabase-setup
install -m 0755 sbin/nli-admin-reset %{buildroot}%{_sbindir}/nli-admin-reset
install -m 0644 supabase/nli_schema.sql %{buildroot}%{_datadir}/nli/supabase/nli_schema.sql
install -m 0640 configs/controller.json configs/controller.env configs/geoip.env %{buildroot}%{_sysconfdir}/nli/
install -m 0644 systemd/nli-controller-agent.service systemd/nli-controller-bootstrap.service systemd/nli-geoip-update.service systemd/nli-geoip-update.timer %{buildroot}/usr/lib/systemd/system/
cp -a web/. %{buildroot}%{_datadir}/nli/webview_ver20/
find %{buildroot}%{_datadir}/nli/webview_ver20 -type d -exec chmod 0755 {} +
find %{buildroot}%{_datadir}/nli/webview_ver20 -type f -exec chmod 0644 {} +
install -m 0644 README.md %{buildroot}%{_docdir}/%{name}/README.md
install -m 0644 V20_UPES_GPRS_ARCHITECTURE_AND_ACCEPTANCE.md V20_R1_TEST_RESULTS.md V20_R1_QRW_OPERATOR_TEST_GUIDE.md V20_QUARANTINE_RESOLUTION_WIZARD.md NLI_V20_UPES_GPRS_ARCHITECTURE.svg %{buildroot}%{_docdir}/%{name}/
install -m 0644 metrics/README.md metrics/metric-catalog.json metrics/prometheus-scrape.yml metrics/grafana-dashboard.json %{buildroot}%{_datadir}/nli/metrics/

%pre
getent group nli >/dev/null || groupadd -r nli
getent passwd nli >/dev/null || useradd -r -g nli -d /var/lib/nli-controller -s /sbin/nologin nli

%post
config_file=%{_sysconfdir}/nli/controller.json
if grep -Eq '"web_dir"[[:space:]]*:[[:space:]]*"/usr/share/nli/webview_ver[15678]"' "$config_file"; then
  sed -i -E 's#/usr/share/nli/webview_ver[15678](["/])#/usr/share/nli/webview_ver20\1#g' "$config_file"
fi
secret_file=%{_sysconfdir}/nli/controller.env
chown root:nli "$secret_file"
chmod 0640 "$secret_file"
systemctl daemon-reload >/dev/null 2>&1 || :
systemctl enable nli-controller-bootstrap.service >/dev/null 2>&1 || :
# The GeoIP timer remains disabled until an administrator accepts the upstream
# license in /etc/nli/geoip.env and deliberately enables it.
if [ -s /etc/nli/controller-bootstrap.env ]; then systemctl start nli-controller-bootstrap.service >/dev/null 2>&1 || :; fi

%preun
if [ "$1" -eq 0 ]; then
  systemctl disable --now nli-controller-bootstrap.service nli-controller-agent.service >/dev/null 2>&1 || :
fi

%postun
systemctl daemon-reload >/dev/null 2>&1 || :

%files
%attr(0755,root,root) %{_bindir}/nli-controller-agent
%attr(0755,root,root) %{_bindir}/nli
%attr(0755,root,root) %{_sbindir}/nli-controller-setup
%attr(0755,root,root) %{_sbindir}/nli-supabase-setup
%attr(0755,root,root) %{_sbindir}/nli-admin-reset
%dir %attr(0750,root,nli) %{_sysconfdir}/nli
%config(noreplace) %attr(0640,root,nli) %{_sysconfdir}/nli/controller.json
%config(noreplace) %attr(0640,root,nli) %{_sysconfdir}/nli/controller.env
%config(noreplace) %attr(0640,root,nli) %{_sysconfdir}/nli/geoip.env
%ghost %attr(0600,root,root) %{_sysconfdir}/nli/controller-bootstrap.env
%attr(0644,root,root) /usr/lib/systemd/system/nli-controller-agent.service
%attr(0644,root,root) /usr/lib/systemd/system/nli-controller-bootstrap.service
%attr(0644,root,root) /usr/lib/systemd/system/nli-geoip-update.service
%attr(0644,root,root) /usr/lib/systemd/system/nli-geoip-update.timer
%dir %attr(0755,root,root) %{_datadir}/nli
%dir %attr(0755,root,root) %{_datadir}/nli/webview_ver20
%attr(0644,root,root) %{_datadir}/nli/webview_ver20/*
%dir %attr(0755,root,root) %{_datadir}/nli/supabase
%attr(0644,root,root) %{_datadir}/nli/supabase/nli_schema.sql
%dir %attr(0755,root,root) %{_datadir}/nli/metrics
%attr(0644,root,root) %{_datadir}/nli/metrics/README.md
%attr(0644,root,root) %{_datadir}/nli/metrics/metric-catalog.json
%attr(0644,root,root) %{_datadir}/nli/metrics/prometheus-scrape.yml
%attr(0644,root,root) %{_datadir}/nli/metrics/grafana-dashboard.json
%dir %attr(0750,nli,nli) /var/lib/nli-controller
%doc %{_docdir}/%{name}/README.md
%doc %{_docdir}/%{name}/V20_UPES_GPRS_ARCHITECTURE_AND_ACCEPTANCE.md
%doc %{_docdir}/%{name}/V20_R1_TEST_RESULTS.md
%doc %{_docdir}/%{name}/V20_R1_QRW_OPERATOR_TEST_GUIDE.md
%doc %{_docdir}/%{name}/V20_QUARANTINE_RESOLUTION_WIZARD.md
%doc %{_docdir}/%{name}/NLI_V20_UPES_GPRS_ARCHITECTURE.svg

%changelog
* Thu Oct 29 2026 Nexora Engineering <engineering@nexora.local> - 20.0.0-1
- Fix exact parser-bundle approval, guided quarantine recovery, live source progress, and resilient TLS synchronization.

* Wed Oct 28 2026 Nexora Engineering <engineering@nexora.local> - 19.0.0-8
- Previous production baseline retained for upgrade and rollback acceptance.
