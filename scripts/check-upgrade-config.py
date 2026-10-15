#!/usr/bin/env python3
"""Refuse an upgrade if upstream startup would rewrite a legacy configuration."""
import json
import pathlib
import sys


def validate(config):
    if not isinstance(config, dict):
        raise ValueError('Expected a JSON configuration object.')
    retired = {'relay', 'android', 'location', 'excluded_domains'} & config.keys()
    if retired or not config.get('final') or config.get('final') == 'proxy':
        raise ValueError('Legacy migration would be required; update refused to preserve configuration.')
    if 'gateway' not in config or 'dns' not in config:
        raise ValueError('Expected the v0.13 gateway/dns configuration schema.')


if __name__ == '__main__':
    config = json.loads(pathlib.Path(sys.argv[1]).read_text(encoding='utf-8-sig'))
    validate(config)
    print('Configuration preflight passed; original file was not modified.')
