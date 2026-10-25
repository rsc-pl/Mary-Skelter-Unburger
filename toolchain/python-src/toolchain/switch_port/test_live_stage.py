"""Synthetic no-game-bytes tests for dynamic translation and safety boundaries."""
from __future__ import annotations
import gzip
import hashlib
import json
from pathlib import Path
import tempfile
import unittest
from unittest.mock import patch
from . import live_stage as mod


class LiveStageTest(unittest.TestCase):
    def setUp(self):
        self.tmp=tempfile.TemporaryDirectory();self.addCleanup(self.tmp.cleanup)
        self.base=Path(self.tmp.name)
        self.root=self.base/'root';(self.root/'translations').mkdir(parents=True)
        self.maps=self.base/'maps';self.maps.mkdir()
        self.native=self.base/'native.jsonl';self.output=self.base/'edit.jsonl'
        name='MS2_STORY.jsonl';self.fname=name
        self.pc={'id':'row-1','fields':[{'LOCALIZED':'Original','EN':'Original','mapping_status':'EXACT_SOURCE'}]}
        self.old={'record_key':'MS2_SWITCH_WESTERN/a071/g0/c0/d0/r0/f0',
                  'text':'Original','raw_hex':b'Original'.hex(),'encoding':'utf-8',
                  'source_dataset':'MS2_SWITCH_WESTERN', 'decode_errors':[],
                  'repack_metadata':{'blob_type':'gnl'}, 'archive_number':71}
        self.link={'target_key':self.old['record_key'],'old_text_sha256':mod.text_digest('Original'),
                   'pc_file':name,'pc_record_id':'row-1','field_ordinal':0,'proof':'TEST_EXACT'}
        self.write_pc()
        for extra in mod.FILES['ms2']:
            if extra!=self.fname:
                (self.root/'translations'/extra).write_text('')
        self.native.write_text(json.dumps(self.old)+'\n');self.write_map()

    def write_pc(self):
        (self.root/'translations'/self.fname).write_text(json.dumps(self.pc)+'\n')

    def write_map(self):
        data=(json.dumps(self.link)+'\n').encode()
        with gzip.open(self.maps/'ms2-stable-alignment.jsonl.gz','wb') as f:f.write(data)
        gz=(self.maps/'ms2-stable-alignment.jsonl.gz').read_bytes()
        manifest={'schema_version':1,'release_ready':False,'games':{
            'ms2':{'native_records_sha256':mod.digest(self.native.read_bytes()),
                   'map_file':'ms2-stable-alignment.jsonl.gz','map_sha256':mod.digest(gz),
                   'mapped_fields':1}}}
        (self.maps/'manifest.json').write_text(json.dumps(manifest))

    def test_pc_edit_applies_automatically_without_new_map(self):
        v=mod.stage('ms2',self.root,self.native,self.output,self.maps)
        self.assertEqual(v['edited_fields'],0)
        self.pc['fields'][0]['EN']='New corrected line!';self.write_pc()
        v=mod.stage('ms2',self.root,self.native,self.output,self.maps)
        self.assertEqual(v['edited_fields'],1)
        self.assertEqual(list(mod.rows(self.output))[0]['text'],'New corrected line!')
        self.assertEqual(v['edited_archives'],[71])
        self.assertEqual(len(list(mod.rows(self.output))),1)

    def test_source_original_cannot_drift(self):
        self.pc['fields'][0]['LOCALIZED']='Not the original';self.write_pc()
        with self.assertRaisesRegex(ValueError,'original localized source drift'):
            mod.stage('ms2',self.root,self.native,self.output,self.maps)
        self.assertFalse(self.output.exists())

    def test_native_records_cannot_drift(self):
        self.native.write_text(self.native.read_text()+'\n')
        with self.assertRaisesRegex(ValueError,'records SHA mismatch'):
            mod.stage('ms2',self.root,self.native,self.output,self.maps)
        self.assertFalse(self.output.exists())

    def test_unsafe_encoding_fails_closed_without_previous_output_damage(self):
        self.old['encoding']='ascii';self.native.write_text(json.dumps(self.old)+'\n');self.write_map()
        self.pc['fields'][0]['EN']='Niebezpieczne ł';self.write_pc()
        self.output.write_text('KEEP_EXISTING_DATA')
        with self.assertRaisesRegex(ValueError,'strict encoding failure'):
            mod.stage('ms2',self.root,self.native,self.output,self.maps)
        self.assertEqual(self.output.read_text(),'KEEP_EXISTING_DATA')

    def test_duplicate_map_rejected(self):
        with gzip.open(self.maps/'ms2-stable-alignment.jsonl.gz','wb') as f:
            f.write((json.dumps(self.link)+'\n') .encode()*2)
        man=json.loads((self.maps/'manifest.json').read_text());man['games']['ms2']['map_sha256']=mod.digest((self.maps/'ms2-stable-alignment.jsonl.gz').read_bytes());man['games']['ms2']['mapped_fields']=2
        (self.maps/'manifest.json').write_text(json.dumps(man))
        with self.assertRaisesRegex(ValueError,'duplicate/malformed'):
            mod.stage('ms2',self.root,self.native,self.output,self.maps)

    def test_source_hash_in_map_must_match_both_sides(self):
        self.link['old_text_sha256']='f'*64;self.write_map()
        with self.assertRaisesRegex(ValueError,'original localized source drift'):
            mod.stage('ms2',self.root,self.native,self.output,self.maps)

if __name__=='__main__': unittest.main()
