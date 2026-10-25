"""No guessing in native Switch translation candidate alignment."""
import importlib.util
import json
import tempfile
import unittest
from pathlib import Path

SCRIPT = Path(__file__).resolve().parents[1] / 'switch_port' / 'prepare.py'
spec = importlib.util.spec_from_file_location('switch_text_prepare', SCRIPT)
port = importlib.util.module_from_spec(spec)
spec.loader.exec_module(port)


class TextPortTests(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory()
        self.addCleanup(self.tmp.cleanup)
        self.root = Path(self.tmp.name)
        (self.root/'translations').mkdir()
        for f in port.TRANSLATIONS['ms2']:
            (self.root/'translations'/f).write_text('', encoding='utf-8')
        self.west = self.root/'western.jsonl'
        self.edits = self.root/'edits.jsonl'
        self.report = self.root/'report.json'

    def translations(self, entries):
        with (self.root/'translations/MS2_STORY.jsonl').open('w', encoding='utf-8') as out:
            for i, (old, new) in enumerate(entries):
                row={'game':'MS2', 'id':f'T{i}', 'fields':[{'LOCALIZED':old,'EN':new,
                    'kind':'text','mapping_status':'EXACT_STRUCTURAL_TRIPLET'}]}
                out.write(json.dumps(row)+'\n')

    def western(self, entries):
        with self.west.open('w', encoding='utf-8') as out:
            for i, old in enumerate(entries):
                row={'record_key':f'MS2_SWITCH_WESTERN/a020/g00000/c0/d000100/r{i:05d}/f000',
                    'source_dataset':'MS2_SWITCH_WESTERN','game':'MS2','platform':'Switch',
                    'text':old, 'raw_hex':old.encode().hex(), 'encoding':'utf-8',
                    'decode_errors':0, 'repack_metadata':{'blob_type':'gnl'}}
                out.write(json.dumps(row)+'\n')

    def run_plan(self):
        return port.plan(self.root,'ms2', self.west,self.edits,self.report)

    def test_unique_transfer_exact_raw_and_source_unchanged(self):
        self.translations([('One rare baseline phrase','Correct honorific, Name-san'),
                           ('Another baseline','Improved English')])
        self.western(['One rare baseline phrase','Some other line','Another baseline'])
        before=self.west.read_bytes()
        rep=self.run_plan()
        self.assertEqual(rep['counts']['safe_exact_unique_fields'],2)
        self.assertEqual([r['text'] for r in port.read_jsonl(self.edits)],
            ['Correct honorific, Name-san','Improved English'])
        self.assertEqual(self.west.read_bytes(), before)
        self.assertFalse(rep['full_text_port_complete'])

    def test_duplicate_pc_baseline_never_mapped_by_guess(self):
        self.translations([('Hello.', 'Oh, hello.'),('Hello.','Hello there.')])
        self.western(['Hello.'])
        rep=self.run_plan()
        self.assertEqual(rep['counts'].get('safe_exact_unique_fields',0),0)
        self.assertEqual(self.edits.read_text(), '')

    def test_duplicate_western_records_not_guessed(self):
        self.translations([('Unique in PC','Changed')])
        self.western(['Unique in PC','Unique in PC'])
        self.assertEqual(self.run_plan()['counts'].get('safe_exact_unique_fields',0),0)

    def test_empty_corpus_does_not_mark_release_complete(self):
        self.translations([])
        self.western([])
        self.assertFalse(self.run_plan()['full_text_port_complete'])

    def test_wrong_dataset_rejected(self):
        self.translations([('a','b')])
        self.western(['a'])
        self.west.write_text(self.west.read_text().replace('MS2_SWITCH_WESTERN','MSF_SWITCH_WESTERN'))
        with self.assertRaisesRegex(ValueError,'non-Western-Switch'):
            self.run_plan()

    def test_no_overwrite_native_source(self):
        self.translations([('a','b')])
        self.western(['a'])
        with self.assertRaisesRegex(ValueError,'overwrite'):
            port.plan(self.root,'ms2',self.west,self.west,self.report)

if __name__ == '__main__':
    unittest.main()
