from contextlib import closing
import json
from pathlib import Path
import sqlite3
import subprocess
import sys
import tempfile
import unittest

SCRIPT=Path(__file__).with_name('production-update.sh').read_text()
CODE=SCRIPT.split("<<'PYINTERRUPTED'\n",1)[1].split('\nPYINTERRUPTED',1)[0]
class InterruptedUpdateTests(unittest.TestCase):
    def test_only_unchanged_pre_migration_state_can_resume(self):
        for invalid in (None,'schema','proxy','network','config','running'):
            with self.subTest(invalid=invalid), tempfile.TemporaryDirectory() as tmp:
                root=Path(tmp).resolve(); project='xboard-production-internal'; name=project+'_default'; rev='646878f202eb5e66bffdbde35f55771aca149d2f'
                db=sqlite3.connect(root/'xboard.db'); db.execute('PRAGMA user_version='+('68' if invalid=='schema' else '67')); db.close()
                app={'Id':'app','State':{'Status':'exited','Running':invalid=='running'},'Config':{'Labels':{'com.docker.compose.project':project,'org.opencontainers.image.revision':rev}},'NetworkSettings':{'Networks':{name:{}}},'Mounts':[{'Destination':'/var/lib/xboard','Type':'volume','Name':project+'_data','Source':str(root)}]}
                proxy={'State':{'Running':True},'NetworkSettings':{'Networks':{name:{}} if invalid=='proxy' else {'other':{}}}}
                net={'Name':name,'Labels':{'com.docker.compose.project':project},'Containers':{'foreign':{}} if invalid=='network' else {}}
                vol={'Name':project+'_data','Driver':'local','Options':None,'Labels':{'com.docker.compose.project':project},'Mountpoint':str(root)}
                for filename,data in [('containers.json',[app,proxy]),('network.json',[net]),('volume.json',[vol])]:
                    (root/filename).write_text(json.dumps(data))
                (root/'.env').write_text('\n'.join(['XBOARD_IMAGE=xboard-go:'+('bad' if invalid=='config' else rev),'COMPOSE_PROJECT_NAME='+project,'XBOARD_PORT=7080','XBOARD_BIND_ADDRESS=127.0.0.1','XBOARD_PANEL_URL=https://fast.hjy.ca:8443']))
                result=subprocess.run([sys.executable,'-',str(root/'containers.json'),str(root/'network.json'),str(root/'volume.json'),str(root/'.env'),rev,project],input=CODE,text=True,capture_output=True)
                if invalid is None: self.assertEqual(result.returncode,0,result.stderr)
                else: self.assertNotEqual(result.returncode,0)
                with closing(sqlite3.connect(root/'xboard.db')) as db:
                    self.assertEqual(db.execute('PRAGMA user_version').fetchone()[0],68 if invalid=='schema' else 67)
if __name__=='__main__': unittest.main()
