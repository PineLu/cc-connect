"""Unit tests for the project-scoped ACP backend switcher (no real launchd operations)."""
import importlib.util
import json
from pathlib import Path
import tempfile
import unittest
from unittest import mock

HERE=Path(__file__).parent
spec=importlib.util.spec_from_file_location("cc_acp_switch",HERE/"cc-acp-switch.py")
switch=importlib.util.module_from_spec(spec)
spec.loader.exec_module(switch)

def config_text(data):
    return ('language = "zh"\n'
            f'data_dir = "{data}"\n'
            '[[projects]]\n'
            'name = "other-project"\n'
            '[projects.agent]\n'
            'type = "claudecode"\n'
            '[projects.agent.options]\n'
            'work_dir = "/tmp/other"\n'
            '[[projects]]\n'
            'name = "tujia-feishu-hermes"\n'
            '[projects.agent]\n'
            'type = "acp"\n'
            '[projects.agent.options]\n'
            'work_dir = "/tmp/acp-sandbox"\n'
            'cmd = "hermes"\n'
            'args = ["-p", "tujia", "acp"]\n'
            'extension = "hermes"\n'
            'mode = "dont_ask"\n'
            'display_name = "Hermes (tujia)"\n'
            'model_command = "/models"\n'
            'compress_command = "/compress"\n'
            '[[projects.platforms]]\n'
            'type = "feishu"\n'
            '[projects.platforms.options]\n'
            'app_id = "other-secret-stays"\n'
            'app_secret = "secret-stays"\n')

class TestSwitcher(unittest.TestCase):
    def setUp(self):
        self.tmp=tempfile.TemporaryDirectory()
        self.addCleanup(self.tmp.cleanup)
        self.dir=Path(self.tmp.name)
        self.config=self.dir/"config.toml"
        self.config.write_text(config_text(self.dir))
        self.plist=self.dir/"service.plist"
        self.plist.write_text("fake")
        self.raw=self.config.read_bytes()
        _,self.parsed,self.project=switch.get_config(self.config)
        self.session=switch.session_path(self.dir,self.project["agent"]["options"])
        self.session.parent.mkdir(parents=True,exist_ok=True)
        self.previous=json.dumps({"sessions":{"hermes-s1":{"agent_session_id":"hermes-session","agent_type":"acp"}}}).encode()
        self.session.write_bytes(self.previous)

    def test_target_only_patch_and_historical_hermes_options(self):
        state,current=switch.profile_and_active(self.project["agent"]["options"],None)
        self.assertEqual(current,"hermes")
        changed=switch.patch_config(self.raw,state["profiles"]["codebuddy"])
        original=switch.get_config(self.config)[1]
        update=switch.tomllib.loads(changed.decode())
        self.assertEqual(update["projects"][0],original["projects"][0])
        self.assertEqual(update["projects"][1]["platforms"],original["projects"][1]["platforms"])
        self.assertEqual(update["projects"][1]["agent"]["options"]["cmd"],"/opt/homebrew/bin/codebuddy")
        self.assertNotIn("model_command",update["projects"][1]["agent"]["options"])
        self.assertNotIn("compress_command",update["projects"][1]["agent"]["options"])
        self.assertEqual(state["profiles"]["hermes"]["model_command"],"/models")
        self.assertIn('app_secret = "secret-stays"',changed.decode())
        back=switch.patch_config(changed,state["profiles"]["hermes"])
        self.assertEqual(switch.tomllib.loads(back.decode()),original)

    def test_cline_profile_uses_generic_acp_with_explicit_approvals(self):
        state, active = switch.profile_and_active(self.project["agent"]["options"], None)
        self.assertEqual(active, "hermes")
        self.assertEqual(set(state["profiles"]), set(switch.BACKENDS))
        cline = state["profiles"]["cline"]
        self.assertEqual(cline["cmd"], "/opt/homebrew/bin/cline")
        self.assertEqual(cline["args"], ["--acp", "--auto-approve", "false"])
        self.assertEqual(cline["extension"], "generic")
        self.assertNotIn("model_command", cline)
        self.assertNotIn("compress_command", cline)
        new = switch.patch_config(self.raw, cline)
        newobj = switch.tomllib.loads(new.decode())
        self.assertEqual(newobj["projects"][0], self.parsed["projects"][0])
        self.assertEqual(newobj["projects"][1]["platforms"],
                         self.parsed["projects"][1]["platforms"])

    def test_existing_three_profiles_migrate_in_memory_without_writes(self):
        definitions, _ = switch.profile_and_active(self.project["agent"]["options"], None)
        legacy = {
            "active": "codebuddy", "previous": "qodercn",
            "profiles": {k: v for k, v in definitions["profiles"].items()
                         if k != "cline"},
        }
        codebuddy_raw = switch.patch_config(self.raw, definitions["profiles"]["codebuddy"])
        self.config.write_bytes(codebuddy_raw)
        root, _ = switch.private_root(switch.get_config(self.config)[1])
        root.mkdir(parents=True)
        state_file = root / "state.json"
        state_file.write_text(json.dumps(legacy))
        old_state_bytes = state_file.read_bytes()
        options = switch.get_config(self.config)[2]["agent"]["options"]
        state, active = switch.profile_and_active(options, switch.load_state(root))
        self.assertEqual(active, "codebuddy")
        self.assertEqual(state["previous"], "qodercn")
        self.assertEqual(state["profiles"]["codebuddy"], legacy["profiles"]["codebuddy"])
        self.assertIn("cline", state["profiles"])
        self.assertIn("codex", state["profiles"])
        self.assertEqual(state_file.read_bytes(), old_state_bytes)
        with mock.patch.object(switch, "binary", return_value="/tmp/fake"):
            result = switch.use(self.config, self.plist, "cline", dry_run=True)
        self.assertIn("codebuddy -> cline", result)
        self.assertEqual(self.config.read_bytes(), codebuddy_raw)
        self.assertEqual(state_file.read_bytes(), old_state_bytes)
        self.assertEqual(self.session.read_bytes(), self.previous)

    def test_cline_session_isolation_and_rollback(self):
        contexts = (mock.patch.object(switch, "binary", return_value="/tmp/fake"),
                    mock.patch.object(switch, "service"),
                    mock.patch.object(switch, "await_state"),
                    mock.patch.object(switch, "service_state",
                                      return_value=("running", 1234)))
        for item in contexts:
            item.start()
        self.addCleanup(lambda: [item.stop() for item in reversed(contexts)])
        self.assertIn("hermes -> cline",
                      switch.use(self.config, self.plist, "cline",
                                 yes=True, probe_target=False))
        root, _ = switch.private_root(self.parsed)
        self.assertEqual((root / "sessions/hermes.json").read_bytes(), self.previous)
        self.assertFalse(self.session.exists())
        cline_state = json.dumps({"sessions": {
            "cline": {"agent_session_id": "cline-private", "agent_type": "acp"}}}).encode()
        self.session.write_bytes(cline_state)
        self.assertIn("cline -> hermes",
                      switch.use(self.config, self.plist, "hermes",
                                 yes=True, probe_target=False))
        self.assertEqual((root / "sessions/cline.json").read_bytes(), cline_state)
        self.assertEqual(self.session.read_bytes(), self.previous)
        self.assertEqual(json.loads((root / "state.json").read_text())["previous"], "cline")
        self.assertIn("hermes -> cline",
                      switch.use(self.config, self.plist, "cline",
                                 yes=True, probe_target=False))
        self.assertEqual(self.session.read_bytes(), cline_state)
        self.assertEqual(json.loads((root / "state.json").read_text())["active"], "cline")

    def test_codex_acp_profile_and_target_only_patch(self):
        state, active = switch.profile_and_active(self.project["agent"]["options"], None)
        self.assertEqual(active, "hermes")
        self.assertEqual(set(state["profiles"]), set(switch.BACKENDS))
        codex = state["profiles"]["codex"]
        self.assertEqual(codex["cmd"], "/opt/homebrew/bin/codex-acp")
        self.assertEqual(codex["args"], [])
        self.assertEqual(codex["extension"], "generic")
        self.assertNotIn("model_command", codex)
        self.assertNotIn("compress_command", codex)
        changed = switch.patch_config(self.raw, codex)
        obj = switch.tomllib.loads(changed.decode())
        self.assertEqual(obj["projects"][0], self.parsed["projects"][0])
        self.assertEqual(obj["projects"][1]["platforms"],
                         self.parsed["projects"][1]["platforms"])
        self.assertEqual(obj["projects"][1]["agent"]["options"]["cmd"], codex["cmd"])
        self.assertEqual(obj["projects"][1]["agent"]["options"]["args"], [])
        restored = switch.patch_config(changed, state["profiles"]["hermes"])
        self.assertEqual(switch.tomllib.loads(restored.decode()), self.parsed)

    def test_four_backend_state_migrates_codex_without_rewrites(self):
        definitions, _ = switch.profile_and_active(self.project["agent"]["options"], None)
        old_state = {
            "active": "cline", "previous": "codebuddy",
            "profiles": {k: v for k, v in definitions["profiles"].items()
                         if k != "codex"},
        }
        cline_raw = switch.patch_config(self.raw, old_state["profiles"]["cline"])
        self.config.write_bytes(cline_raw)
        root, _ = switch.private_root(switch.get_config(self.config)[1])
        root.mkdir(parents=True)
        managed = root / "state.json"
        managed.write_text(json.dumps(old_state))
        old_bytes = managed.read_bytes()
        options = switch.get_config(self.config)[2]["agent"]["options"]
        state, active = switch.profile_and_active(options, switch.load_state(root))
        self.assertEqual(active, "cline")
        self.assertEqual(state["previous"], "codebuddy")
        self.assertEqual(state["profiles"]["cline"], old_state["profiles"]["cline"])
        self.assertIn("codex", state["profiles"])
        self.assertEqual(managed.read_bytes(), old_bytes)
        with mock.patch.object(switch, "binary", return_value="/tmp/fake"):
            plan = switch.use(self.config, self.plist, "codex", dry_run=True)
        self.assertIn("cline -> codex", plan)
        self.assertEqual(managed.read_bytes(), old_bytes)
        self.assertEqual(self.config.read_bytes(), cline_raw)
        self.assertEqual(self.session.read_bytes(), self.previous)

    def test_codex_session_isolation_and_rollback(self):
        contexts = (mock.patch.object(switch, "binary", return_value="/tmp/fake"),
                    mock.patch.object(switch, "service"),
                    mock.patch.object(switch, "await_state"),
                    mock.patch.object(switch, "service_state",
                                      return_value=("running", 1234)))
        for item in contexts:
            item.start()
        self.addCleanup(lambda: [item.stop() for item in reversed(contexts)])
        self.assertIn("hermes -> codex",
                      switch.use(self.config, self.plist, "codex",
                                 yes=True, probe_target=False))
        root, _ = switch.private_root(self.parsed)
        self.assertEqual((root / "sessions/hermes.json").read_bytes(), self.previous)
        self.assertFalse(self.session.exists())
        codex_data = json.dumps({"sessions": {
            "codex": {"agent_session_id": "codex-private",
                      "agent_type": "acp"}}}).encode()
        self.session.write_bytes(codex_data)
        self.assertIn("codex -> hermes",
                      switch.use(self.config, self.plist, "hermes",
                                 yes=True, probe_target=False))
        self.assertEqual((root / "sessions/codex.json").read_bytes(), codex_data)
        self.assertEqual(self.session.read_bytes(), self.previous)
        self.assertIn("hermes -> codex",
                      switch.use(self.config, self.plist, "codex",
                                 yes=True, probe_target=False))
        self.assertEqual(self.session.read_bytes(), codex_data)
        self.assertEqual(json.loads((root / "state.json").read_text())["active"], "codex")

    def test_workspace_session_files_fail_closed(self):
        extra=self.dir/"sessions"/(switch.PROJECT+"_ws_deadbeef.json")
        extra.write_text(json.dumps({"sessions":{"x":{"agent_session_id":"other-backend"}}}))
        with mock.patch.object(switch,"binary",return_value="/tmp/fake"):
            with self.assertRaisesRegex(switch.Refuse,"workspace-specific"):
                switch.use(self.config,self.plist,"codebuddy",dry_run=True)
        self.assertEqual(self.config.read_bytes(),self.raw)
        self.assertEqual(self.session.read_bytes(),self.previous)

    def test_multi_workspace_mode_fail_closed(self):
        parsed=switch.tomllib.loads(self.raw.decode())
        parsed["projects"][1]["mode"]="multi-workspace"
        special=self.raw.replace(b'name = "tujia-feishu-hermes"',b'name = "tujia-feishu-hermes"\nmode = "multi-workspace"')
        obj=switch.tomllib.loads(special.decode())
        with self.assertRaisesRegex(switch.Refuse,"multi-workspace"):
            switch.ensure_single_workspace(obj,self.dir)

    def test_expanded_home_workdir_uses_same_session_file(self):
        opts=self.project["agent"]["options"].copy()
        opts["work_dir"]="~/test-acp"
        home_path=switch.session_path(self.dir,opts)
        opts["work_dir"]=str(Path.home()/"test-acp")
        self.assertEqual(home_path,switch.session_path(self.dir,opts))
        opts["work_dir"]=chr(36)+"{HOME}/test-acp"
        self.assertEqual(home_path,switch.session_path(self.dir,opts))
        opts["work_dir"]=chr(36)+"{UNKNOWN_CUSTOM_DIR}/work"
        with self.assertRaisesRegex(switch.Refuse,"manual session-path verification"):
            switch.session_path(self.dir,opts)
        self.assertEqual(self.session.read_bytes(),self.previous)

    def test_runtime_workdir_override_refuses_session_switch(self):
        state_path=self.dir/"projects"/(switch.PROJECT+".state.json")
        state_path.parent.mkdir(parents=True)
        state_path.write_text(json.dumps({"work_dir_override":"/tmp/other-work-dir"}))
        with self.assertRaisesRegex(switch.Refuse,"work_dir_override"):
            switch.session_path(self.dir,self.project["agent"]["options"])
        with mock.patch.object(switch,"binary",return_value="/tmp/fake"):
            with self.assertRaisesRegex(switch.Refuse,"work_dir_override"):
                switch.use(self.config,self.plist,"codebuddy",dry_run=True)
        self.assertEqual(self.config.read_bytes(),self.raw)
        self.assertEqual(self.session.read_bytes(),self.previous)

    def test_dryrun_does_not_mutate_config_or_sessions(self):
        with mock.patch.object(switch,"binary",return_value="/tmp/fake"),mock.patch.object(switch,"service",side_effect=AssertionError("service touched")):
            result=switch.use(self.config,self.plist,"codebuddy",dry_run=True)
        self.assertIn("PLAN hermes -> codebuddy",result)
        self.assertEqual(self.config.read_bytes(),self.raw)
        self.assertEqual(self.session.read_bytes(),self.previous)
        self.assertFalse(switch.private_root(self.parsed)[0].exists())

    def test_backup_failure_does_not_erase_existing_session(self):
        def fail_atomic(path, content, mode=0o600):
            raise OSError("simulated backup write failure")
        with mock.patch.object(switch,"binary",return_value="/tmp/fake"),mock.patch.object(switch,"service"),mock.patch.object(switch,"await_state"),mock.patch.object(switch,"service_state",return_value=("running",1234)):
            # Allow recovery to use atomic successfully after the initial
            # backup error; otherwise the injected FS outage also prevents
            # service restoration and tests a different failure mode.
            real_atomic=switch.atomic
            calls=0
            def once(path,content,mode=0o600):
                nonlocal calls
                calls+=1
                if calls==1: raise OSError("simulated backup write failure")
                return real_atomic(path,content,mode)
            with mock.patch.object(switch,"atomic",side_effect=once):
                with self.assertRaisesRegex(switch.Refuse,"original service restored"):
                    switch.use(self.config,self.plist,"codebuddy",yes=True,probe_target=False)
        self.assertEqual(self.session.read_bytes(),self.previous)
        self.assertEqual(self.config.read_bytes(),self.raw)

    def test_roundtrip_preserves_vendor_session_snapshots(self):
        args=(mock.patch.object(switch,"binary",return_value="/tmp/fake"),
              mock.patch.object(switch,"service"),
              mock.patch.object(switch,"await_state"),
              mock.patch.object(switch,"service_state",return_value=("running",1234)))
        for ctx in args:ctx.start()
        self.addCleanup(lambda:[ctx.stop() for ctx in reversed(args)])
        switch.use(self.config,self.plist,"codebuddy",yes=True,probe_target=False)
        self.assertFalse(self.session.exists())
        root,_=switch.private_root(self.parsed)
        self.assertEqual((root/"sessions/hermes.json").read_bytes(),self.previous)
        codebuddy_content=json.dumps({"sessions":{"cb":{"agent_session_id":"codebuddy-session","agent_type":"acp"}}}).encode()
        self.session.write_bytes(codebuddy_content)
        switch.use(self.config,self.plist,"hermes",yes=True,probe_target=False)
        self.assertEqual(self.session.read_bytes(),self.previous)
        self.assertEqual((root/"sessions/codebuddy.json").read_bytes(),codebuddy_content)
        self.assertEqual(json.loads((root/"state.json").read_text())["active"],"hermes")

    def test_restart_failure_rolls_back_both_config_and_session(self):
        call_count=0
        def fake_service(plist, action):
            nonlocal call_count
            if action=="start":
                call_count+=1
                if call_count==1: raise switch.Refuse("simulated startup failure")
        with mock.patch.object(switch,"binary",return_value="/tmp/fake"),mock.patch.object(switch,"service",side_effect=fake_service),mock.patch.object(switch,"await_state"),mock.patch.object(switch,"service_state",return_value=("running",1234)):
            with self.assertRaisesRegex(switch.Refuse,"original service restored"):
                switch.use(self.config,self.plist,"codebuddy",yes=True,probe_target=False)
        self.assertEqual(self.config.read_bytes(),self.raw)
        self.assertEqual(self.session.read_bytes(),self.previous)
        self.assertIsNone(switch.load_state(switch.private_root(self.parsed)[0]))
        self.assertEqual(call_count,2)

    def test_interrupted_switch_restores_old_backend_snapshot(self):
        root,_=switch.private_root(self.parsed)
        saved=root/"sessions/hermes.json"
        saved.parent.mkdir(parents=True)
        old_snapshot=json.dumps({"sessions":{"old-hermes":{"agent_session_id":"older-session"}}}).encode()
        saved.write_bytes(old_snapshot)
        def interrupted(plist,action):
            if action=="start":
                raise KeyboardInterrupt("simulated kill")
        with mock.patch.object(switch,"binary",return_value="/tmp/fake"),mock.patch.object(switch,"service",side_effect=interrupted),mock.patch.object(switch,"await_state"),mock.patch.object(switch,"service_state",return_value=("running",1234)):
            with self.assertRaises(KeyboardInterrupt):
                switch.use(self.config,self.plist,"codebuddy",yes=True,probe_target=False)
        self.assertNotEqual(saved.read_bytes(),old_snapshot)
        self.assertEqual(saved.read_bytes(),self.previous)
        self.assertTrue((root/"pending.json").exists())
        with mock.patch.object(switch,"service"),mock.patch.object(switch,"await_state"),mock.patch.object(switch,"service_state",return_value=("running",1234)):
            switch.recover(self.config,self.plist,yes=True)
        self.assertEqual(saved.read_bytes(),old_snapshot)
        self.assertEqual(self.session.read_bytes(),self.previous)
        self.assertEqual(self.config.read_bytes(),self.raw)

    def test_interrupted_after_start_is_recoverable(self):
        def interrupted(plist,action):
            if action=="start":
                raise KeyboardInterrupt("simulate operator kill after startup")
        with mock.patch.object(switch,"binary",return_value="/tmp/fake"),mock.patch.object(switch,"service",side_effect=interrupted),mock.patch.object(switch,"await_state"),mock.patch.object(switch,"service_state",return_value=("running",1234)):
            with self.assertRaises(KeyboardInterrupt):
                switch.use(self.config,self.plist,"codebuddy",yes=True,probe_target=False)
        root,_=switch.private_root(self.parsed)
        self.assertTrue((root/"pending.json").exists())
        with self.assertRaisesRegex(switch.Refuse,"incomplete ACP transaction"):
            switch.ensure_no_pending(root)
        self.assertFalse(self.session.exists())
        self.assertEqual(switch.get_config(self.config)[2]["agent"]["options"]["cmd"],"/opt/homebrew/bin/codebuddy")
        with mock.patch.object(switch,"service"),mock.patch.object(switch,"await_state"),mock.patch.object(switch,"service_state",return_value=("running",1234)):
            self.assertIn("recovered",switch.recover(self.config,self.plist,yes=True))
        self.assertFalse((root/"pending.json").exists())
        self.assertFalse((root/"state.json").exists())
        self.assertEqual(self.config.read_bytes(),self.raw)
        self.assertEqual(self.session.read_bytes(),self.previous)

    def test_manually_modified_profile_fails_closed(self):
        state={"active":"codebuddy","previous":"hermes","profiles":{"hermes":{"cmd":"hermes"},"codebuddy":{"cmd":"fake"},"qodercn":{"cmd":"fake"}}}
        with self.assertRaisesRegex(switch.Refuse,"modified outside switcher"):
            switch.profile_and_active(self.project["agent"]["options"],state)

if __name__=="__main__":
    unittest.main()
