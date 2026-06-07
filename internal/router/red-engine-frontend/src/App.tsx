import { Outlet, Link } from 'react-router';

function App() {
  return (
    <>
      <div className="flex flex-col min-h-screen text-brand font-display">
        <div>
          <div className="flex flex-row gap-4 my-4">
            {/* top bar */}
            <Link
              to={{
                pathname: '/',
              }}
            >
              <div className="flex flex-row gap-4 ml-8">
                <img src="logo.png" className="h-20 w-20" />
                <h1 className="m-auto text-6xl font-black">The RED Engine</h1>
              </div>
            </Link>
            <nav className="flex mx-32">
              <a className="m-auto ">Articles</a>
            </nav>
          </div>
        </div>
        <div className="flex flex-1 flex-row">
          <div className="flex flex-col border-r border-brand p-4">
            {/* Sidebar */}
            <p>Node Access</p>
            <div className="flex flex-row gap-4 align-middle justify-center"></div>
          </div>
          <div className="flex flex-1 flex-col">
            <Outlet />
            {/* For Children */}
          </div>
        </div>
      </div>
    </>
  );
}

export default App;
