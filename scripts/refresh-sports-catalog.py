#!/usr/bin/env python3
"""Refresh the bundled, offline-searchable six-league catalog from primary feeds.
Run from any directory. Review the diff and run go test ./... before committing.
Existing identities are retained across promotions, relocations and provider changes.
"""
import argparse
import concurrent.futures
import datetime
import json
from pathlib import Path
import re
import urllib.parse
import urllib.request

ROUTES = {'MLB': 'baseball/mlb', 'NHL': 'hockey/nhl', 'NBA': 'basketball/nba',
          'NFL': 'football/nfl', 'MLS': 'soccer/usa.1', 'EPL': 'soccer/eng.1'}
# ESPN's NHL IDs differ from the NHL API. Adapted from Sportray's reviewed
# current-franchise catalog; names/branding/logos come from the current feed.
NHL = {'25': (24,'ANA'), '1': (6,'BOS'), '2': (7,'BUF'), '7': (12,'CAR'),
       '29': (29,'CBJ'), '3': (20,'CGY'), '4': (16,'CHI'), '17': (21,'COL'),
       '9': (25,'DAL'), '5': (17,'DET'), '6': (22,'EDM'), '26': (13,'FLA'),
       '8': (26,'LAK'), '30': (30,'MIN'), '10': (8,'MTL'), '11': (1,'NJD'),
       '27': (18,'NSH'), '12': (2,'NYI'), '13': (3,'NYR'), '14': (9,'OTT'),
       '15': (4,'PHI'), '16': (5,'PIT'), '124292': (55,'SEA'), '18': (28,'SJS'),
       '19': (19,'STL'), '20': (14,'TBL'), '21': (10,'TOR'),
       '129764': (68,'UTA'), '22': (23,'VAN'), '37': (54,'VGK'),
       '28': (52,'WPG'), '23': (15,'WSH')}
# Existing Boston keys and display names must not invalidate stored selections.
LEGACY = {('MLB','111'): ('red-sox','Red Sox'), ('NHL','6'): ('bruins','Bruins'),
          ('NFL','17'): ('patriots','Patriots'), ('NBA','2'): ('celtics','Celtics'),
          ('MLS','189'): ('revolution','Revolution')}

def fetch(url):
    request = urllib.request.Request(url, headers={'User-Agent': 'Home-HQ-catalog/1.0'})
    with urllib.request.urlopen(request, timeout=25) as response:
        body = response.read((2 << 20) + 1)
    if len(body) > 2 << 20:
        raise ValueError('Catalog too large')
    return json.loads(body)

def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--from-dir', type=Path, help='Use previously downloaded primary feed JSON for reproducible offline review')
    parser.add_argument('--output', type=Path, default=Path(__file__).resolve().parents[1] / 'internal/sports/catalog.json')
    args = parser.parse_args()
    urls = {league: 'https://site.api.espn.com/apis/site/v2/sports/'+route+'/teams?limit=1000' for league,route in ROUTES.items()}
    urls['MLB-native'] = 'https://statsapi.mlb.com/api/v1/teams?sportId=1'
    def read(item):
        league,url = item
        if args.from_dir:
            filename = {'MLB':'mlb-espn','NHL':'nhl-espn','MLB-native':'mlb'}.get(league,league.lower())
            return league,json.loads((args.from_dir/(filename+'.json')).read_text())
        return league,fetch(url)
    with concurrent.futures.ThreadPoolExecutor(max_workers=7) as pool:
        payloads = dict(pool.map(read, urls.items()))
    mlb = {t['name']: str(t['id']) for t in payloads['MLB-native']['teams'] if t.get('active')}
    if len(mlb) != 30:
        raise ValueError('Review the changed MLB franchise catalog')
    records = []
    for league,route in ROUTES.items():
        members = payloads[league]['sports'][0]['leagues'][0]['teams']
        expected = {'MLB':30,'NHL':32,'NBA':30,'NFL':32,'MLS':30,'EPL':20}[league]
        if len(members) != expected:
            raise ValueError(f'Review changed {league} membership: {len(members)}')
        for member in members:
            t = member['team']
            provider_id,abbr = str(t['id']),t['abbreviation']
            if league == 'MLB':
                provider_id = mlb[t['displayName']]
            if league == 'NHL':
                provider_id,abbr = NHL[provider_id]
                provider_id = str(provider_id)
            key,name = LEGACY.get((league,provider_id),(league.lower()+'-'+provider_id, t['shortDisplayName']))
            logo = t['logos'][0]['href']
            parsed = urllib.parse.urlsplit(logo)
            if parsed.scheme != 'https' or parsed.netloc != 'a.espncdn.com' or not re.fullmatch(r'/i/teamlogos/[a-z]+/500/[a-z0-9_-]+\.png',parsed.path) or parsed.query or parsed.fragment:
                raise ValueError('Review changed logo URL '+logo)
            if not re.fullmatch(r'[A-Z0-9-]{1,12}',abbr):
                raise ValueError('Unexpected abbreviation '+abbr)
            records.append(dict(Key=key,Name=name,FullName=t['displayName'],League=league,
                                ProviderID=provider_id,Route='' if league in ('MLB','NHL') else route,Logo=logo,Abbr=abbr))
    # Retain previously cataloged identities so a roster refresh cannot silently
    # reset a household's selected teams, including relegated Premier League clubs.
    if args.output.exists():
        previous = json.loads(args.output.read_text())['teams']
        by_key = {t['Key']:t for t in previous}
        by_key.update({t['Key']:t for t in records})
        records = list(by_key.values())
    identities = [(t['League'],t['ProviderID']) for t in records]
    if len(set(identities)) != len(records) or len({t['Key'] for t in records}) != len(records):
        raise ValueError('Duplicate catalog identity')
    records.sort(key=lambda t:(t['FullName'].casefold(),t['League']))
    result = {'updated': datetime.datetime.now(datetime.timezone.utc).strftime('%Y-%m-%d'), 'teams':records}
    args.output.write_text(json.dumps(result,ensure_ascii=False,indent=2)+'\n')
    print(f'Prepared {len(records)} teams in {args.output}; review mappings, run tests and commit the catalog.')

if __name__ == '__main__':
    main()
