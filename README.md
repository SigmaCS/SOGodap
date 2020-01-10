# SOGo LDAP Server: SOGodap

SOGodap is an open source LDAP server used to access vCard entries from address books on an [Inverse][inverse] [SOGo][sogo] groupware installation.  Currently [MySQL][mysql] (and by implication [MariaDB][maria]) is the only database supported.

Our Git repository is located at https://github.com/SigmaCS/SOGodap where you can inspect the code and download releases.

Unless otherwise noted, the SOGodap source files are distributed under the Apache License Version 2.0 found in the LICENSE file.

### Installation

Download the latest binary for your Linux platform from our [releases][releases] or clone the repository and build it with `go install github.com/SigmaCS/SOGodap`.

Once you have the binary image, install it as follows:

```shell
cp SOGodap /usr/local/bin/  
chmod +x /usr/local/bin/SOGodap
```

A sample systemd service module to start the server is included in the `Scripts` directory, which can be installed by copying it to  `/etc/systemd/system/sogodap.service` in most Linux distributions.

Once the service file has been installed enable and start the service with:

```shell
systemctl enable sogodap.service
systemctl start sogodap.service
```

### Configuration

The configuration of SOGodap is controlled by a [GNUstep Property List file][plist], similar to the `sogo.conf` used by [SOGo][sogo].  By default this is found in `/etc/sogo/sogodap.conf` but may be overridden by the command `-conf` command-line parameter.

A sample configuration file is provided as `sogodap.conf` in the source code.

The following parameters (case sensitive) are available in the configuration file:

| Parameter        | Usage     |
| --- | --- |
| AuthPass | The password that must be specified in a simple bind operation by the client to connect to query the LDAP server. |
| AuthUser | The username that must be specified in a simple bind operation by the client to connect to query the LDAP server. |
| Filter_`x` | May be specified multiple times where `x` is an LDAP attribute name specified in the search query and the value is a [MySQL REGEXP][regex] pattern used to locate it in the vCard data.  The string `_val_` will be replaced in the regular expression by the search value received from the client. |
| ListenAddress | Defaults to `127.0.0.1` and specifies the IP address upon which the LDAP server will listen for requests. |
| ListenPort | Defaults to `10389` and specifies the port upon which the LDAP server will listen for requests.  Note than ports less than 1024 require additional privileges on most Linux systems. |
| MaxResults | Defaults to `100` and specifies the maximum number of contacts that will be returned by each query.  May be set to `0` for unlimited results assuming the client can support this. |
| SogoConf | Defaults to  `/etc/sogo/sogo.conf` and specifies the location of the SOGo configuration file used to obtain the database credentials. |
| SortAttributes | Default to `1` specifying that the result set will be sorted by the first requested attribute.  Use a larger integer to specify additional attributes to sort by (at the expense of CPU time) or `0` to disable sorting completely. |
| SubtreeLookup | May specify one or more additional SOGo usernames separated by commas.  If the LDAP search has the subtree flag set this will also search the address books of the specified users for matching contacts.  These will be appended to the result set.  This can be useful for shared corporate directories. |

### Usage

SOGodap does not currently implement any over-the-wire encryption or multiple user credentials.  A simple, plain text bind may be used for authentication with the LDAP server.  For this reason it is recommended to restrict the service to internal usage and where possible isolate it on its own VLAN.

To query the address book of user@example.net (`-b "uid=user@example.net"`), plus any configured shared address books (`-s sub`), for surnames beginning Smith and return their name, company and telephone number, run the following:

```shell
ldapsearch -x -D lookup -w "<secret>" -h localhost -p 10389 -b "uid=user@example.net" -s sub "(sn=Smith*)" cn o telephonenumber
```

### Contributing

Please feel free to clone the repository, make changes and then raise a pull request if you feel this may benefit the wider community.  We welcome and will review all such contributions.

File bug reports or feature requests via the issue tracker.

[inverse]: https://inverse.ca/
[maria]: https://mariadb.org/
[mysql]: https://www.mysql.com/
[plist]: http://wiki.gnustep.org/index.php/Property_Lists
[regex]: https://dev.mysql.com/doc/refman/5.7/en/regexp.html
[releases]: https://github.com/SigmaCS/SOGodap/releases
[sogo]: https://sogo.nu/
