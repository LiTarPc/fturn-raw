#!/usr/bin/env python3
"""Linux integration tests: isolated files, mocked systemd/network/downloads, real ELF/ZIP/SHA."""
import base64,hashlib,json,os,pathlib,platform,shlex,shutil,subprocess,tempfile,unittest,zipfile
ROOT=pathlib.Path(__file__).resolve().parent.parent
SCRIPT=ROOT/'scripts/debian/quick-start.sh'
MOCK=r"""#!/usr/bin/env python3
import hashlib,json,os,pathlib,re,shutil,subprocess,sys
prefix=pathlib.Path(os.environ['TEST_PREFIX']);path=prefix/'state.json'
s=json.loads(path.read_text());cmd=pathlib.Path(sys.argv[0]).name;a=sys.argv[1:]
def save(): path.write_text(json.dumps(s))
def run_helper(mode):
    p=prefix/'opt/fturn-raw/raw-firewall'
    if p.exists(): return subprocess.run([str(p),mode]).returncode
    return 0
if cmd=='curl':
    dest=pathlib.Path(a[a.index('-o')+1]);url=a[-1];s['downloads']+=1;save()
    if url.endswith('SERVER-SHA256SUMS'):
        name='fturn-raw-server-1.0.0-linux-'+('arm64' if s.get('arch')=='aarch64' else 'amd64')+'.zip'
        h='0'*64 if s.get('bad_checksum') else hashlib.sha256((prefix/'fixture.zip').read_bytes()).hexdigest()
        dest.write_text(h+'  '+name+'\n')
    else: shutil.copyfile(prefix/'fixture.zip',dest)
elif cmd=='ip':
    if 'route' in a:
        if 'get' in a: print(json.dumps([{'dev':'ens3','prefsrc':'203.0.113.10'}]))
        else: print(json.dumps(s.get('routes',[])))
    elif 'address' in a:
        if not s['active']: sys.exit(1)
        print(json.dumps([{'mtu':s.get('mtu',1420),'addr_info':[{'local':'10.77.0.1','prefixlen':24}]}]))
    elif 'ftraw0' in a: sys.exit(0 if s['active'] else 1)
elif cmd=='ss':
    port=int(a[-1].split(':')[-1])
    if s.get('busy_port')==port: print(f'UNCONN 0 0 *:{port} *:* users:(("other",pid=9000,fd=3))')
    elif s['active'] and s.get('port',56010)==port: print(f'UNCONN 0 0 *:{port} *:* users:(("raw-server",pid=1234,fd=3))')
elif cmd=='sysctl':
    if '-n' in a: print(s['forward'])
    else: s['forward']=int(a[-1].split('=')[-1]);save()
elif cmd=='iptables':
    table=a[a.index('-t')+1]
    op=next(x for x in ['-C','-I','-D'] if x in a);i=a.index(op);chain=a[i+1];args=a[i+2:]
    if op=='-I': assert args.pop(0)=='1'
    rule=[table,chain,*args]
    if op=='-C': sys.exit(0 if rule in s['rules'] else 1)
    if op=='-I':
        s['inserts']=s.get('inserts',0)+1;save()
        if s['inserts']==s.get('fail_rule_at'): sys.exit(7)
        s['rules'].insert(0,rule)
    if op=='-D': s['rules'].remove(rule)
    save()
elif cmd=='systemctl':
    if a[0]=='show':
        prop=next(x for x in a if x.startswith('--property=')).split('=')[1]
        print({'FragmentPath':str(prefix/'systemd/fturn-raw.service') if (prefix/'systemd/fturn-raw.service').exists() else '', 'DropInPaths':s.get('dropins',''), 'MainPID':'1234' if s['active'] else '0'}.get(prop,''))
    elif a[0]=='is-active': sys.exit(0 if s['active'] else 3)
    elif a[0]=='enable':
        s['enabled']=True;save()
        if '--now' in a:
            text=(prefix/'systemd/fturn-raw.service').read_text()
            s['mtu']=int(re.search(r'-mtu (\d+)',text)[1]);s['port']=int(re.search(r'-listen 0.0.0.0:(\d+)',text)[1]);save()
            code=run_helper('up');s=json.loads(path.read_text())
            if code or s.get('fail_start'): sys.exit(1)
            s['active']=True;save()
    elif a[0]=='stop':
        run_helper('down');s=json.loads(path.read_text());s['active']=False;save()
    elif a[0]=='disable': s['enabled']=False;save()
    elif a[0]!='daemon-reload': raise AssertionError(a)
elif cmd=='systemd-analyze':
    assert a[0]=='verify' and pathlib.Path(a[1]).is_file()
    if s.get('fail_verify'): sys.exit(1)
elif cmd=='uname': print(s.get('arch','x86_64'))
elif cmd=='sleep': pass
elif cmd=='apt-get': raise AssertionError('Tests must never install packages')
else: raise AssertionError((cmd,a))
"""
class QuickStart(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        if os.geteuid()!=0: raise RuntimeError('Run with sudo; all writes remain in private temporary directories and all network/systemd calls are mocked')
        cls.fixture_dir=tempfile.TemporaryDirectory(prefix='fturn-quick-start-fixture-')
        cls.fixture=pathlib.Path(cls.fixture_dir.name)/'raw-server'
        c=cls.fixture.with_suffix('.c')
        c.write_text('#include <stdio.h>\n#include <string.h>\nint main(int n,char **v){if(n==2&&!strcmp(v[1],"--version")){puts("FturnRaw server 1.0.0");return 0;}if(n==2&&!strcmp(v[1],"-gen-obf-key")){puts("'+('a'*64)+'");return 0;}return 1;}\n')
        subprocess.run(['cc','-o',str(cls.fixture),str(c)],check=True)
    @classmethod
    def tearDownClass(cls): cls.fixture_dir.cleanup()
    def setUp(self):
        self.temp=tempfile.TemporaryDirectory(prefix='fturn-quick-start-test-')
        self.p=pathlib.Path(self.temp.name)
        for name in ['bin','systemd','sysctl']: (self.p/name).mkdir()
        mock=self.p/'bin/mock';mock.write_text(MOCK);mock.chmod(0o755)
        for cmd in ['curl','ip','ss','sysctl','iptables','systemctl','systemd-analyze','uname','sleep','apt-get']: (self.p/'bin'/cmd).symlink_to(mock)
        with zipfile.ZipFile(self.p/'fixture.zip','w') as z: z.write(self.fixture,'raw-server')
        self.base_rule=['filter','INPUT','-p','tcp','--dport','2022','-j','ACCEPT']
        self.state({'active':False,'enabled':False,'forward':0,'downloads':0,'rules':[self.base_rule],'arch':platform.machine()})
    def tearDown(self): self.temp.cleanup()
    def state(self,data=None):
        p=self.p/'state.json'
        if data is not None: p.write_text(json.dumps(data))
        return json.loads(p.read_text())
    def update(self,**kw):
        s=self.state();s.update(kw);self.state(s)
    def run_script(self,*args,success=True):
        env=os.environ.copy();env.update({'PATH':str(self.p/'bin')+':'+env['PATH'],'TEST_PREFIX':str(self.p)})
        code='source '+shlex.quote(str(SCRIPT))+'\n'
        for name,path in {'INSTALL_DIR':'opt/fturn-raw','CONFIG_DIR':'etc/fturn-raw','UNIT_FILE':'systemd/fturn-raw.service','SYSCTL_FILE':'sysctl/90-fturn-raw-quick-start.conf','LOCK_FILE':'lock'}.items(): code+=name+'='+shlex.quote(str(self.p/path))+'\n'
        code+='require_systemd() { :; }\nrequire_tun() { :; }\nmain "$@"\n'
        r=subprocess.run(['bash','-c',code,'test-quick-start',*args],env=env,text=True,capture_output=True,timeout=35)
        if success: self.assertEqual(r.returncode,0,r.stdout+r.stderr)
        else: self.assertNotEqual(r.returncode,0,r.stdout+r.stderr)
        return r
    def assert_clean_failure(self,forward=0):
        self.assertFalse((self.p/'systemd/fturn-raw.service').exists())
        self.assertFalse((self.p/'opt/fturn-raw/raw-server').exists())
        self.assertFalse((self.p/'sysctl/90-fturn-raw-quick-start.conf').exists())
        s=self.state();self.assertFalse(s['active']);self.assertFalse(s['enabled'])
        self.assertEqual(s['forward'],forward);self.assertEqual(s['rules'],[self.base_rule])
    def test_install_link_repeat_and_uninstall(self):
        r=self.run_script('--public-ip','203.0.113.10','--vk-link','https://vk.ru/call/join/fixture')
        key=self.p/'etc/fturn-raw/raw.key';before=key.read_bytes()
        self.assertEqual(key.stat().st_mode&0o777,0o600)
        link=next(x for x in r.stdout.splitlines() if x.startswith('fturnraw://'))
        data=link.removeprefix('fturnraw://');wire=json.loads(base64.urlsafe_b64decode(data+'='*(-len(data)%4)))
        self.assertEqual(wire,{'v':1,'server':'203.0.113.10:56010','vk':'https://vk.ru/call/join/fixture','key':'a'*64,'mtu':1420,'streams':10,'mode':'full'})
        s=self.state();self.assertEqual(len(s['rules']),14);self.assertTrue(s['enabled']);self.assertEqual(s['downloads'],2)
        self.run_script();self.assertEqual(self.state()['downloads'],2);self.assertEqual(key.read_bytes(),before)
        self.run_script('--check');self.assertEqual(key.read_bytes(),before)
        self.run_script('--uninstall');self.assertEqual(key.read_bytes(),before)
        self.assertFalse(self.state()['active']);self.assertFalse(self.state()['enabled']);self.assertEqual(self.state()['rules'],[self.base_rule])
        self.assertEqual(self.state()['forward'],1);self.assertFalse((self.p/'systemd/fturn-raw.service').exists())
    def test_custom_port_mtu_interface_and_pool(self):
        self.run_script('--port','60000','--mtu','1280','--devices','5','--interface','enp0s3')
        c=json.loads((self.p/'etc/fturn-raw/quick-start.json').read_text())
        self.assertEqual((c['port'],c['mtu'],c['devices'],c['interface']),(60000,1280,5,'enp0s3'))
        self.assertEqual(len(self.state()['rules']),22)
        for n in range(2,7): self.assertTrue(any('10.77.0.'+str(n)+'/32' in r and 'MASQUERADE' in r for r in self.state()['rules']))
        self.run_script('--devices','5')
        self.run_script('--devices','6',success=False)
        self.assertTrue(self.state()['active'])
    def test_dry_run_changes_no_files_or_state(self):
        before=self.state();self.run_script('--check');self.assertEqual(self.state(),before)
        self.assertFalse((self.p/'lock').exists());self.assertFalse((self.p/'etc').exists())
    def test_legacy_install_untouched(self):
        old=self.p/'systemd/fturn-raw.service';old.write_text('legacy-service')
        self.run_script(success=False);self.assertEqual(old.read_text(),'legacy-service');self.assertEqual(self.state()['downloads'],0)
    def test_port_conflict_is_not_stopped(self):
        self.update(busy_port=56010);self.run_script(success=False);self.assertEqual(self.state()['downloads'],0);self.assert_clean_failure()
    def test_overlapping_subnet_rejected(self):
        self.update(routes=[{'dst':'10.77.0.0/24','dev':'other-vpn'}]);self.run_script(success=False);self.assertEqual(self.state()['downloads'],0);self.assert_clean_failure()
    def test_bad_checksum_does_not_install(self):
        self.update(bad_checksum=True);self.run_script(success=False);self.assert_clean_failure();self.assertFalse((self.p/'etc/fturn-raw/raw.key').exists())
    def test_start_failure_removes_partial_rules_and_keeps_existing_key(self):
        key=self.p/'etc/fturn-raw/raw.key';key.parent.mkdir(parents=True);key.write_text('b'*64+'\n');key.chmod(0o600)
        self.update(fail_start=True);self.run_script(success=False);self.assert_clean_failure();self.assertEqual(key.read_text(),'b'*64+'\n')
    def test_start_failure_preserves_prior_forwarding(self):
        self.update(fail_start=True,forward=1);self.run_script(success=False);self.assert_clean_failure(forward=1)
    def test_unit_verification_failure_rolls_back(self):
        self.update(fail_verify=True);self.run_script(success=False);self.assert_clean_failure()
    def test_foreign_architecture_is_rejected(self):
        self.update(arch='x86_64' if platform.machine() in ('aarch64','arm64') else 'aarch64');self.run_script(success=False);self.assert_clean_failure()
    def test_partial_firewall_failure_rolls_back(self):
        self.update(fail_rule_at=4);self.run_script(success=False);self.assert_clean_failure()
    def test_firewall_start_stop_is_idempotent(self):
        self.run_script()
        env=os.environ.copy();env.update({'PATH':str(self.p/'bin')+':'+env['PATH'],'TEST_PREFIX':str(self.p)})
        helper=self.p/'opt/fturn-raw/raw-firewall'
        before=self.state()['rules']
        for mode in ['up','up']: subprocess.run([str(helper),mode],env=env,check=True)
        self.assertEqual(self.state()['rules'],before)
        for mode in ['down','down']: subprocess.run([str(helper),mode],env=env,check=True)
        self.assertEqual(self.state()['rules'],[self.base_rule])
    def test_writable_install_directory_is_rejected(self):
        folder=self.p/'opt/fturn-raw';folder.mkdir(parents=True);folder.chmod(0o777)
        note=folder/'notes.txt';note.write_text('preserve')
        self.run_script(success=False);self.assertEqual(note.read_text(),'preserve');self.assertEqual(self.state()['downloads'],0)
    def test_unknown_architecture_is_rejected_before_download(self):
        self.update(arch='mips');self.run_script(success=False);self.assert_clean_failure();self.assertEqual(self.state()['downloads'],0)
    def test_symlink_key_is_not_followed(self):
        target=self.p/'unrelated';target.write_text('do-not-touch')
        key=self.p/'etc/fturn-raw/raw.key';key.parent.mkdir(parents=True);key.symlink_to(target)
        self.run_script(success=False);self.assertEqual(target.read_text(),'do-not-touch');self.assertEqual(self.state()['downloads'],0)
    def test_edited_managed_install_is_not_deleted(self):
        self.run_script();unit=self.p/'systemd/fturn-raw.service';unit.write_text(unit.read_text()+'# custom\n')
        self.run_script('--uninstall',success=False);self.assertTrue(self.state()['active']);self.assertTrue(unit.exists())
    def test_arguments_rejected_before_install(self):
        for args in [('--port','0'),('--devices','65'),('--mtu','1501'),('--interface','bad name'),('--vk-link','https://vk.ru/call/join/fixture'),('--public-ip','127.0.0.1'),('--public-ip','203.0.113.10','--vk-link','https://evil.example/call/join/fixture')]:
            with self.subTest(args=args): self.run_script(*args,success=False)
        self.assertEqual(self.state()['downloads'],0)
if __name__=='__main__': unittest.main(verbosity=2)
