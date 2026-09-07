"""Exercise the real bot request paths without contacting an upstream service."""

import io
import json
import os
import unittest
from unittest.mock import patch

import issue_triage_agent
import pr_review_agent


class ModelConfigTests(unittest.TestCase):
    agents = (issue_triage_agent, pr_review_agent)

    def test_invalid_base_url_fails_before_network(self):
        for agent in self.agents:
            for base_url in (None, "", "  ", "ftp://example.com/v1", "/v1", "https:///v1"):
                with self.subTest(agent=agent.__name__, base_url=base_url):
                    env = {"OPENAI_API_KEY": "test-key"}
                    if base_url is not None:
                        env["OPENAI_BASE_URL"] = base_url
                    with patch.dict(os.environ, env, clear=True), patch(
                        "urllib.request.urlopen", side_effect=AssertionError("network must not run")
                    ) as urlopen:
                        with self.assertRaisesRegex(RuntimeError, "OPENAI_BASE_URL"):
                            agent.call_model("system", "payload")
                        urlopen.assert_not_called()

    def test_configured_http_endpoints_preserve_request_contract(self):
        body = json.dumps({"choices": [{"message": {"content": "ok"}}]}).encode()
        for agent in self.agents:
            for base_url in ("https://relay.example.com/v1/", "http://localhost:8317/v1"):
                with self.subTest(agent=agent.__name__, base_url=base_url):
                    env = {
                        "OPENAI_API_KEY": "test-key",
                        "OPENAI_BASE_URL": base_url,
                        "OPENAI_MODEL": "configured-model",
                    }
                    with patch.dict(os.environ, env, clear=True), patch(
                        "urllib.request.urlopen", return_value=io.BytesIO(body)
                    ) as urlopen:
                        self.assertEqual(agent.call_model("system", "payload"), "ok")
                    urlopen.assert_called_once()
                    request = urlopen.call_args.args[0]
                    self.assertEqual(request.full_url, base_url.rstrip("/") + "/chat/completions")
                    self.assertEqual(request.get_method(), "POST")
                    self.assertEqual(request.get_header("Authorization"), "Bearer test-key")
                    self.assertEqual(json.loads(request.data)["model"], "configured-model")


if __name__ == "__main__":
    unittest.main()
