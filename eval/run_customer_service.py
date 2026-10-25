#!/usr/bin/env python3
"""Execute frozen merchant customer cases via Go; captures stay outside Git."""
from __future__ import annotations

import argparse
import hashlib
import http.cookiejar
import json
import os
import ssl
import time
import urllib.error
import urllib.request
from pathlib import Path
from uuid import uuid4

ROOT=Path(__file__).resolve().parents[1]


def digest(data):return hashlib.sha256(data).hexdigest()


def run(args):
    contract=ROOT/'eval/customer-service-cases.v1.json'
    data=json.loads(contract.read_text())
    for file,checksum in data['source_files'].items():
        if digest((ROOT/file).read_bytes())!=checksum:raise ValueError('source_changed')
    out=args.capture_dir.resolve()
    if out.exists() or out.is_relative_to(ROOT):raise ValueError('new restricted capture directory required')
    out.mkdir(parents=True,mode=0o700)
    rows=[]
    last_session=0.0
    for case in data['cases']:
        time.sleep(max(0, 6.5-(time.monotonic()-last_session)))
        last_session=time.monotonic()
        jar=http.cookiejar.CookieJar()
        opener=urllib.request.build_opener(urllib.request.HTTPCookieProcessor(jar),urllib.request.HTTPSHandler(context=ssl.create_default_context(cafile=str(args.ca))))
        csrf=''
        def call(path,body=None):
            raw=None if body is None else json.dumps(body).encode()
            headers={'Origin':args.base_url,'Content-Type':'application/json','X-CSRF-Token':csrf,'Idempotency-Key':str(uuid4())}  # noqa: B023
            request=urllib.request.Request(args.base_url+path,data=raw,headers=headers)
            try:
                with opener.open(request,timeout=10) as r:return json.load(r)  # noqa: B023
            except urllib.error.HTTPError as error:
                if error.code!=429:raise
                time.sleep(60)
                with opener.open(request,timeout=10) as r:return json.load(r)  # noqa: B023
        csrf=call('/v1/customer/session',{'shop_slug':args.shop})['csrf_token']
        conversation=call('/v1/customer/conversations',{})['conversation_id']
        started=time.monotonic()
        submissions=[]
        decisions=[]
        events=[]
        for query in [case['query']]+([data['clarification_followup']['query']] if case['case_id']=='clarify' else []):
            submit=call('/v1/customer/conversations/'+conversation+'/messages',{'text':query})
            submissions.append(submit)
            deadline=time.monotonic()+75
            while time.monotonic()<deadline:
                snapshot=call('/v1/customer/conversations/'+conversation)
                if not snapshot['conversation'].get('active_execution_id') and any(m['source']!='customer' for m in snapshot['messages']):break
                time.sleep(.3)
            else:raise ValueError('execution_timeout')
            # Bind the observed decision to durable Go events, never to the
            # expected behavior in the contract. SSE carries checked messages.
            with opener.open(args.base_url+'/v1/customer/conversations/'+conversation+'/events',timeout=30) as stream:
                event={}
                for raw in stream:
                    line=raw.decode().strip()
                    if line.startswith('event:'):event['type']=line[6:].strip()
                    elif line.startswith('data:'):event['data']=json.loads(line[5:].strip())
                    elif not line and 'data' in event:
                        events.append(event)
                        payload=event['data']
                        if event.get('type')=='completed' and payload.get('execution_id')==submit['execution_id']:
                            decisions.append(payload['behavior'].removeprefix('REPLY_BEHAVIOR_'));break
                        if event.get('type')=='state' and payload.get('state')=='WAITING_HUMAN':
                            decisions.append('HANDOFF');break
                        event={}
                else:raise ValueError('missing_persisted_decision')
        answer=[m for m in snapshot['messages'] if m['source']=='assistant']
        capture={'case':case,'submissions':submissions,'snapshot':snapshot,'events':events,'actual_behaviors':decisions,'elapsed_ms':round((time.monotonic()-started)*1000)}
        path=out/(case['case_id']+'.json')
        with os.fdopen(os.open(path,os.O_WRONLY|os.O_CREAT|os.O_EXCL,0o600),'w') as f:json.dump(capture,f,ensure_ascii=False,indent=2)
        rows.append({'case_id':case['case_id'],'query_sha256':case['query_sha256'],'capture_sha256':digest(path.read_bytes()),'conversation_id':conversation,'execution_ids':[s['execution_id'] for s in submissions], 'state':snapshot['conversation']['state'],'actual_behaviors':decisions,'elapsed_ms':capture['elapsed_ms'],'citation_ids':[c['evidence_id'] for m in answer for c in m['citations']],'answer_sha256':[digest(m['text'].encode()) for m in answer],'review_status':'not_reviewed'})
        print(json.dumps({'case_id':case['case_id'],'state':snapshot['conversation']['state'],'review_status':'not_reviewed'}),flush=True)
        write_report(args.report,contract,rows)
    write_report(args.report,contract,rows)


def write_report(path,contract,rows):
    report={'schema_version':1,'status':'NOT_PASSED','contract_sha256':digest(contract.read_bytes()),'rows':rows,'reviewer_kind':'agent','human_review_completed':False,'real_service_acceptance':False,'note':'Execution and persisted decisions require separate semantic review'}
    with os.fdopen(os.open(path,os.O_WRONLY|os.O_CREAT|os.O_TRUNC,0o600),'w') as f:json.dump(report,f,indent=2)


if __name__=='__main__':
    p=argparse.ArgumentParser(description=__doc__)
    p.add_argument('--base-url',required=True);p.add_argument('--ca',type=Path,required=True);p.add_argument('--shop',default='my-shop');p.add_argument('--capture-dir',type=Path,required=True);p.add_argument('--report',type=Path,required=True)
    args=p.parse_args()
    try:run(args)
    except Exception:  # noqa: BLE001 - CLI boundary maps any failure to a fixed message
        raise SystemExit('NOT_PASSED customer evaluation; inspect protected runtime and capture') from None
