%{!?nli_version:%global nli_version 20.0.0}
%{!?nli_release:%global nli_release 1}
Name:           nli-client
Version:        %{nli_version}
Release:        %{nli_release}%{?dist}
Summary:        Nexora Log Intelligence Ver20 parsing client
License:        LicenseRef-Proprietary
URL:            https://github.com/vjay136/nexora_log_intelligence_ver20.git
Source0:        %{name}-%{version}.tar.gz
ExclusiveArch:  x86_64
Provides:       nli-agent = %{version}-%{release}
Obsoletes:      nli-agent < %{version}
Requires(pre):  shadow-utils
Requires(post): systemd
Requires(preun): systemd
Requires(postun): systemd
Requires:       python3 curl openssl
AutoReqProv:    no
%global debug_package %{nil}
%global _build_id_links none
%global __os_install_post %{nil}

%description
NLI v20 client containing the source-derived static parser agent, CLI, benchmark
utility, and restricted fleet-control sidecar. The nli_clinet_ver20.rpm spelling
is a byte-identical distribution filename alias, not a second RPM package.

%prep
%autosetup -n %{name}-%{version}

%build
# Binaries are built, tested, and provenance-checked by the pinned Go builder.

%install
install -d %{buildroot}%{_bindir} %{buildroot}%{_sbindir} %{buildroot}%{_sysconfdir}/nli/parsers
install -d %{buildroot}/usr/lib/systemd/system %{buildroot}%{_docdir}/%{name}
install -d %{buildroot}%{_datadir}/nli/metrics
install -d %{buildroot}/var/lib/nli/parsers/quarantine %{buildroot}/var/lib/nli/parser-assets %{buildroot}/var/lib/nli/spool
install -d %{buildroot}/var/lib/nli-control-client %{buildroot}/var/cache/nli/packages
install -d %{buildroot}/var/log/nli/input %{buildroot}/var/log/nli/output
install -m 0755 bin/nli bin/nli-agent bin/nli-client-control bin/nli-bench %{buildroot}%{_bindir}/
install -m 0755 sbin/nli-client-setup %{buildroot}%{_sbindir}/nli-client-setup
install -m 0640 configs/nli.json configs/nli.env configs/client-control.json configs/client-control.env %{buildroot}%{_sysconfdir}/nli/
echo "%%dir %%attr(0750,root,nli) %{_sysconfdir}/nli/parsers" > parser-files.list
for parser in configs/parsers/*.json; do
  [ -f "$parser" ] || continue
  base=$(basename "$parser")
  install -m 0644 "$parser" %{buildroot}%{_sysconfdir}/nli/parsers/
  echo "%%config(noreplace) %%attr(0644,root,nli) %{_sysconfdir}/nli/parsers/$base" >> parser-files.list
done
install -m 0644 configs/outputs.example.json %{buildroot}%{_docdir}/%{name}/outputs.example.json
install -m 0644 systemd/nli-agent.service systemd/nli-client-control.service systemd/nli-client-bootstrap.service %{buildroot}/usr/lib/systemd/system/
install -m 0644 README.md %{buildroot}%{_docdir}/%{name}/README.md
install -m 0644 PARSER_CATALOG.md %{buildroot}%{_docdir}/%{name}/PARSER_CATALOG.md
install -m 0644 V20_UPES_GPRS_ARCHITECTURE_AND_ACCEPTANCE.md V20_R1_TEST_RESULTS.md V20_R1_QRW_OPERATOR_TEST_GUIDE.md V20_QUARANTINE_RESOLUTION_WIZARD.md NLI_V20_UPES_GPRS_ARCHITECTURE.svg %{buildroot}%{_docdir}/%{name}/
install -m 0644 metrics/README.md metrics/metric-catalog.json metrics/prometheus-scrape.yml metrics/grafana-dashboard.json %{buildroot}%{_datadir}/nli/metrics/

%pre
getent group nli >/dev/null || groupadd -r nli
getent passwd nli >/dev/null || useradd -r -g nli -d /var/lib/nli -s /sbin/nologin nli

%post
systemctl daemon-reload >/dev/null 2>&1 || :
install -d -o nli -g nli -m 0750 /var/log/nli/input /var/log/nli/output
for family in auto imap pop webtop mta smtp-in smtp-out pabd capd pss; do
  [ -e "/var/log/nli/input/$family.log" ] || install -o nli -g nli -m 0640 /dev/null "/var/log/nli/input/$family.log"
done
if [ -f /var/lib/nli-control-client/install.id ]; then chgrp nli /var/lib/nli-control-client/install.id; chmod 0640 /var/lib/nli-control-client/install.id; fi
if [ -f /var/lib/nli-control-client/node.token ]; then chgrp nli /var/lib/nli-control-client/node.token; chmod 0640 /var/lib/nli-control-client/node.token; fi
if [ ! -e /var/log/nli/quarantine.jsonl ]; then install -o nli -g nli -m 0640 /dev/null /var/log/nli/quarantine.jsonl; fi
systemctl enable nli-client-bootstrap.service >/dev/null 2>&1 || :
if [ -s /etc/nli/bootstrap.env ]; then systemctl start nli-client-bootstrap.service >/dev/null 2>&1 || :; fi

%preun
if [ "$1" -eq 0 ]; then
  systemctl disable --now nli-client-bootstrap.service nli-client-control.service nli-agent.service >/dev/null 2>&1 || :
fi

%postun
systemctl daemon-reload >/dev/null 2>&1 || :

%files -f parser-files.list
%attr(0755,root,root) %{_bindir}/nli
%attr(0755,root,root) %{_bindir}/nli-agent
%attr(0755,root,root) %{_bindir}/nli-client-control
%attr(0755,root,root) %{_bindir}/nli-bench
%attr(0755,root,root) %{_sbindir}/nli-client-setup
%dir %attr(0750,root,nli) %{_sysconfdir}/nli
%config(noreplace) %attr(0640,root,nli) %{_sysconfdir}/nli/nli.json
%config(noreplace) %attr(0640,root,nli) %{_sysconfdir}/nli/nli.env
%config(noreplace) %attr(0640,root,nli) %{_sysconfdir}/nli/client-control.json
%config(noreplace) %attr(0640,root,nli) %{_sysconfdir}/nli/client-control.env
%ghost %attr(0600,root,root) %{_sysconfdir}/nli/bootstrap.env
%attr(0644,root,root) /usr/lib/systemd/system/nli-agent.service
%attr(0644,root,root) /usr/lib/systemd/system/nli-client-control.service
%attr(0644,root,root) /usr/lib/systemd/system/nli-client-bootstrap.service
%dir %attr(0750,nli,nli) /var/lib/nli
%dir %attr(0750,root,nli) /var/lib/nli/parsers
%dir %attr(0750,nli,nli) /var/lib/nli/parsers/quarantine
%dir %attr(0750,root,nli) /var/lib/nli/parser-assets
%dir %attr(0750,nli,nli) /var/lib/nli/spool
%dir %attr(0755,root,root) %{_datadir}/nli
%dir %attr(0755,root,root) %{_datadir}/nli/metrics
%attr(0644,root,root) %{_datadir}/nli/metrics/README.md
%attr(0644,root,root) %{_datadir}/nli/metrics/metric-catalog.json
%attr(0644,root,root) %{_datadir}/nli/metrics/prometheus-scrape.yml
%attr(0644,root,root) %{_datadir}/nli/metrics/grafana-dashboard.json
%dir %attr(0750,root,nli) /var/lib/nli-control-client
%dir %attr(0750,root,nli) /var/cache/nli
%dir %attr(0750,root,nli) /var/cache/nli/packages
%dir %attr(0750,nli,nli) /var/log/nli
%dir %attr(0750,nli,nli) /var/log/nli/input
%dir %attr(0750,nli,nli) /var/log/nli/output
%ghost %attr(0640,nli,nli) /var/log/nli/quarantine.jsonl
%doc %{_docdir}/%{name}/README.md
%doc %{_docdir}/%{name}/outputs.example.json
%doc %{_docdir}/%{name}/PARSER_CATALOG.md
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
