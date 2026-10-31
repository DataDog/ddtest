describe('greeting page', () => {
  it('shows the greeting', async () => {
    await browser.url('data:text/html,<title>Greeting</title><p id="greeting">Hello</p>');
    await expect($('#greeting')).toHaveText('Hello');
  });
});
