# Ladilala

[![Download for Windows](https://img.shields.io/badge/Download_for_Windows-0078D4?style=for-the-badge&logo=windows&logoColor=white)](https://github.com/71zone/ladilala/releases/latest)

Ladilala is a Windows app that does two jobs.

- **Browser profiles.** Each profile is a separate browser with its own proxy, its own identity and its own saved logins. Profiles do not mix.
- **Web macros.** Record a task once in the browser. Ladilala repeats it for every row of your Excel, CSV, JSON or REST data. Use it to fill forms.

The app is in Vietnamese by default. English is built in. It updates itself.

## Download

Version 1.1.0, Windows 64-bit. Use the button above, or open the [latest release](https://github.com/71zone/ladilala/releases/latest).

- `ladilala-1.1.0-windows-amd64-setup.exe`: the installer. Use this one first.
- `ladilala-1.1.0-windows-amd64.exe`: the portable program. No install needed.

Windows may show a SmartScreen warning. The files are not code-signed yet. Two of 71 antivirus engines flag the program as a possible threat. Both flags come from machine-learning rules and are likely false alarms. See the [VirusTotal result](https://www.virustotal.com/gui/file/4e8db0d2e8350dea35cef32c2eade86702f3b783d8e75904936236bb54c23164). Checksums are in `SHA256SUMS.txt` in the release.

## Good to know

- Windows 11 is tested. Windows 10 is not.
- Google Sheets is not supported yet.

## For developers

See [docs/development.md](docs/development.md) for the code layout, the build and the tests.
