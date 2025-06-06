## nova cell install

Install a cell from source

### Synopsis

Installs cell configurations from a given source.

This command will install the cell depending on the provided source, where source can be either a file or a url.
The content can be either in json or yaml format and should comply with the cell configuration specification (see 'http://host/api/v1/ui/#/operations/updateCell').

If the cell already exists, the configuration will be updated according to the provided specification.

NOTE: When executing this command, the CLI configuration will be updated with the cell name from the provided file.
This is the equivalent of running 'nova config set cellname <name>' with the cell name from the given file.


```
nova cell install <source> [flags]
```

### Examples

```

	# install from json file
	nova cell install path/to/app.json

	# install from yaml file
	nova cell install path/to/cell.yaml
	# or
	nova cell install path/to/cell.yml

	# install from json or yaml from remote location
	nova cell install https://somehost.com/path/to/cell.yaml?latest=true

```

### Options

```
  -h, --help   help for install
```

### SEE ALSO

* [nova cell](nova_cell.md)	 - Manage cell setups

###### Auto generated by spf13/cobra on 30-Apr-2025
